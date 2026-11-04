// Package apiauth provides optional bearer-token authentication for calls to
// claim-machinery-api.
//
// Authentication is off by default: without configuration no Authorization
// header is sent. Three modes are supported, in this order of precedence:
//
//   - a static bearer token (CLAIM_API_TOKEN / --api-token)
//   - OAuth2 client credentials (CLAIM_API_OIDC_TOKEN_URL, CLAIM_API_OIDC_CLIENT_ID,
//     CLAIM_API_OIDC_CLIENT_SECRET, optional CLAIM_API_OIDC_SCOPE), where the
//     token is fetched once and cached in-process until it expires.
//   - a cached interactive login (`claims login`, device flow), looked up per
//     API URL in the credentials file and refreshed when it expires.
//
// Token values are never included in errors or log output produced by this
// package.
package apiauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// Environment variable names.
const (
	EnvToken            = "CLAIM_API_TOKEN"
	EnvOIDCTokenURL     = "CLAIM_API_OIDC_TOKEN_URL"
	EnvOIDCClientID     = "CLAIM_API_OIDC_CLIENT_ID"
	EnvOIDCClientSecret = "CLAIM_API_OIDC_CLIENT_SECRET"
	EnvOIDCScope        = "CLAIM_API_OIDC_SCOPE"
)

// tokenFetchTimeout bounds a single request to the OIDC token endpoint.
const tokenFetchTimeout = 30 * time.Second

// Config holds the auth settings. Empty fields mean "not configured".
type Config struct {
	Token            string
	OIDCTokenURL     string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCScope        string // space- or comma-separated
}

// FromEnv reads the config from the CLAIM_API_* environment variables.
func FromEnv() Config {
	return Config{
		Token:            os.Getenv(EnvToken),
		OIDCTokenURL:     os.Getenv(EnvOIDCTokenURL),
		OIDCClientID:     os.Getenv(EnvOIDCClientID),
		OIDCClientSecret: os.Getenv(EnvOIDCClientSecret),
		OIDCScope:        os.Getenv(EnvOIDCScope),
	}
}

// Merge returns c with every empty field filled from fallback. Use it as
// flags.Merge(FromEnv()) so that flags take precedence over the environment.
func (c Config) Merge(fallback Config) Config {
	pick := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	return Config{
		Token:            pick(c.Token, fallback.Token),
		OIDCTokenURL:     pick(c.OIDCTokenURL, fallback.OIDCTokenURL),
		OIDCClientID:     pick(c.OIDCClientID, fallback.OIDCClientID),
		OIDCClientSecret: pick(c.OIDCClientSecret, fallback.OIDCClientSecret),
		OIDCScope:        pick(c.OIDCScope, fallback.OIDCScope),
	}
}

// Mode describes which authentication mode a config resolves to.
type Mode string

const (
	ModeNone              Mode = "none"
	ModeStaticToken       Mode = "static-token"
	ModeClientCredentials Mode = "client-credentials"
	ModeLogin             Mode = "login"
)

// Mode returns the effective mode. The static token wins over client credentials.
func (c Config) Mode() Mode {
	switch {
	case c.Token != "":
		return ModeStaticToken
	case c.OIDCTokenURL != "" || c.OIDCClientID != "" || c.OIDCClientSecret != "":
		return ModeClientCredentials
	default:
		return ModeNone
	}
}

// Validate reports an incomplete client-credentials configuration.
func (c Config) Validate() error {
	if c.Mode() != ModeClientCredentials {
		return nil
	}
	var missing []string
	if c.OIDCTokenURL == "" {
		missing = append(missing, EnvOIDCTokenURL+" (--api-oidc-token-url)")
	}
	if c.OIDCClientID == "" {
		missing = append(missing, EnvOIDCClientID+" (--api-oidc-client-id)")
	}
	if c.OIDCClientSecret == "" {
		missing = append(missing, EnvOIDCClientSecret+" (--api-oidc-client-secret)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("incomplete OIDC client-credentials config for claim-machinery-api, missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

// Authenticator injects the Authorization header into API requests.
// A nil *Authenticator is valid and adds nothing.
type Authenticator struct {
	mode  Mode
	token string             // static token
	src   oauth2.TokenSource // client-credentials or login source
}

// New builds an Authenticator from cfg. It returns (nil, nil) when auth is
// not configured. httpClient is used for token endpoint requests (nil means
// a default client with a timeout).
func New(cfg Config, httpClient *http.Client) (*Authenticator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Mode() {
	case ModeStaticToken:
		return &Authenticator{mode: ModeStaticToken, token: cfg.Token}, nil
	case ModeClientCredentials:
		if httpClient == nil {
			httpClient = &http.Client{Timeout: tokenFetchTimeout}
		}
		cc := &clientcredentials.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			TokenURL:     cfg.OIDCTokenURL,
			Scopes:       splitScopes(cfg.OIDCScope),
		}
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
		// clientcredentials' TokenSource is a ReuseTokenSource: the token is
		// fetched on first use and cached until shortly before it expires.
		return &Authenticator{mode: ModeClientCredentials, src: cc.TokenSource(ctx)}, nil
	default:
		return nil, nil
	}
}

// Mode returns the authentication mode (ModeNone for a nil Authenticator).
func (a *Authenticator) Mode() Mode {
	if a == nil {
		return ModeNone
	}
	return a.mode
}

// Wrap returns a RoundTripper that adds the bearer token to every request.
// For a nil Authenticator, base is returned unchanged (nil means
// http.DefaultTransport).
func (a *Authenticator) Wrap(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if a == nil {
		return base
	}
	return &transport{auth: a, base: base}
}

// HTTPClient returns an http.Client with the given timeout whose transport
// adds the bearer token.
func (a *Authenticator) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: a.Wrap(nil)}
}

// BearerToken returns the token that is sent to the API (fetching or
// refreshing it if needed). Callers must not print it.
func (a *Authenticator) BearerToken() (string, error) {
	if a == nil {
		return "", errors.New("no API authentication configured")
	}
	return a.bearer()
}

// Resolve returns the authenticator for apiURL: cfg (static token or client
// credentials, from flags/env) wins; otherwise a login cached in store for
// apiURL is used; otherwise nil (no auth). A store that cannot be read is
// reported through warn and treated as "no login".
func Resolve(cfg Config, apiURL string, store *Store, warn func(string)) (*Authenticator, error) {
	if cfg.Mode() != ModeNone || store == nil {
		return New(cfg, nil)
	}
	cred, ok, err := store.Get(apiURL)
	if err != nil {
		warn(fmt.Sprintf("ignoring cached logins: %v", err))
		return nil, nil
	}
	if !ok {
		return nil, nil
	}
	hc, err := LoginHTTPClient(cred.CAFile)
	if err != nil {
		return nil, fmt.Errorf("login for %s: %w", cred.APIURL, err)
	}
	return NewLogin(store, cred, hc), nil
}

func (a *Authenticator) bearer() (string, error) {
	switch a.mode {
	case ModeStaticToken:
		return a.token, nil
	case ModeLogin:
		tok, err := a.src.Token()
		if err != nil {
			return "", err // *LoginExpiredError, already says what to do
		}
		return tok.AccessToken, nil
	}
	tok, err := a.src.Token()
	if err != nil {
		return "", fmt.Errorf("fetching OIDC client-credentials token from %s: %w", EnvOIDCTokenURL, err)
	}
	return tok.AccessToken, nil
}

type transport struct {
	auth *Authenticator
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.auth.bearer()
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	// RoundTrippers must not modify the caller's request.
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(r)
}

// StatusHint returns a human-readable hint for 401/403 responses from
// claim-machinery-api, or "" for other status codes.
func StatusHint(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized:
		return fmt.Sprintf("claim-machinery-api requires authentication (or rejected the token): "+
			"run `claims login`, or set %s (--api-token) or the OIDC client credentials %s, %s, %s and optionally %s",
			EnvToken, EnvOIDCTokenURL, EnvOIDCClientID, EnvOIDCClientSecret, EnvOIDCScope)
	case http.StatusForbidden:
		return "claim-machinery-api accepted the token but denied the request " +
			"(missing order role, or an owner outside your groups?): `claims whoami` shows the roles in your token"
	default:
		return ""
	}
}

func splitScopes(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\t' || r == '\n'
	})
}
