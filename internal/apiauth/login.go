package apiauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	"golang.org/x/oauth2"
)

// Interactive login (claims login): the OAuth2 device authorization grant
// (RFC 8628) against the lab ZITADEL. The tokens are cached per API URL in a
// Store, so `render` and `encrypt` pick them up without further settings.

// Environment variables for `claims login`.
const (
	EnvLoginIssuer   = "CLAIM_API_LOGIN_ISSUER"
	EnvLoginClientID = "CLAIM_API_LOGIN_CLIENT_ID"
	EnvLoginScope    = "CLAIM_API_LOGIN_SCOPE"
	EnvLoginCAFile   = "CLAIM_API_LOGIN_CA_FILE"
)

// DefaultLoginScope is requested when no scope is configured. offline_access
// asks for a refresh token, so a login outlives the access token.
const DefaultLoginScope = "openid profile email offline_access"

// LoginConfig holds the settings of the device flow. Empty fields mean "not
// configured".
type LoginConfig struct {
	Issuer   string
	ClientID string
	Scope    string // space- or comma-separated
	CAFile   string // extra PEM roots for the issuer (lab ZITADEL: Vault PKI)
}

// LoginConfigFromEnv reads the CLAIM_API_LOGIN_* environment variables.
func LoginConfigFromEnv() LoginConfig {
	return LoginConfig{
		Issuer:   os.Getenv(EnvLoginIssuer),
		ClientID: os.Getenv(EnvLoginClientID),
		Scope:    os.Getenv(EnvLoginScope),
		CAFile:   os.Getenv(EnvLoginCAFile),
	}
}

// Merge returns c with every empty field filled from fallback.
func (c LoginConfig) Merge(fallback LoginConfig) LoginConfig {
	pick := func(a, b string) string {
		if a != "" {
			return a
		}
		return b
	}
	return LoginConfig{
		Issuer:   pick(c.Issuer, fallback.Issuer),
		ClientID: pick(c.ClientID, fallback.ClientID),
		Scope:    pick(c.Scope, fallback.Scope),
		CAFile:   pick(c.CAFile, fallback.CAFile),
	}
}

// Validate reports missing issuer or client ID.
func (c LoginConfig) Validate() error {
	var missing []string
	if c.Issuer == "" {
		missing = append(missing, EnvLoginIssuer+" (--issuer)")
	}
	if c.ClientID == "" {
		missing = append(missing, EnvLoginClientID+" (--client-id)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("login needs %s", strings.Join(missing, " and "))
	}
	return nil
}

// Scopes returns the configured scopes, or DefaultLoginScope.
func (c LoginConfig) Scopes() []string {
	if s := splitScopes(c.Scope); len(s) > 0 {
		return s
	}
	return splitScopes(DefaultLoginScope)
}

// Provider holds the endpoints from the issuer's OIDC discovery document.
type Provider struct {
	Issuer                      string `json:"issuer"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	UserinfoEndpoint            string `json:"userinfo_endpoint"`
}

// LoginHTTPClient returns a client for the issuer that trusts the system
// roots plus the PEM bundle in caFile (if set).
func LoginHTTPClient(caFile string) (*http.Client, error) {
	if caFile == "" {
		return &http.Client{Timeout: tokenFetchTimeout}, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("reading CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("CA file %s holds no PEM certificate", caFile)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: tokenFetchTimeout, Transport: tr}, nil
}

// Discover reads <issuer>/.well-known/openid-configuration.
func Discover(ctx context.Context, hc *http.Client, issuer string) (Provider, error) {
	u := strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Provider{}, fmt.Errorf("OIDC discovery: %w", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Provider{}, fmt.Errorf("OIDC discovery at %s: %w", u, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Provider{}, fmt.Errorf("OIDC discovery at %s: HTTP %d", u, resp.StatusCode)
	}
	var p Provider
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&p); err != nil {
		return Provider{}, fmt.Errorf("OIDC discovery at %s: %w", u, err)
	}
	if p.TokenEndpoint == "" {
		return Provider{}, fmt.Errorf("OIDC discovery at %s: no token_endpoint", u)
	}
	if p.DeviceAuthorizationEndpoint == "" {
		return Provider{}, fmt.Errorf("issuer %s offers no device_authorization_endpoint (device flow not enabled?)", issuer)
	}
	return p, nil
}

func oauthConfig(clientID, tokenURL, deviceURL string, scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID: clientID,
		Scopes:   scopes,
		Endpoint: oauth2.Endpoint{
			TokenURL:      tokenURL,
			DeviceAuthURL: deviceURL,
			// Native apps are public clients: client_id goes in the form body.
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

// DeviceLogin runs the device flow: it requests a device code, hands the
// verification URL and user code to show, and polls the token endpoint until
// the user has approved (or ctx ends / the code expires).
func DeviceLogin(ctx context.Context, hc *http.Client, p Provider, cfg LoginConfig, show func(*oauth2.DeviceAuthResponse)) (*oauth2.Token, error) {
	oc := oauthConfig(cfg.ClientID, p.TokenEndpoint, p.DeviceAuthorizationEndpoint, cfg.Scopes())
	ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	da, err := oc.DeviceAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("device authorization: %w", err)
	}
	show(da)
	tok, err := oc.DeviceAccessToken(ctx, da)
	if err != nil {
		return nil, fmt.Errorf("waiting for approval: %w", err)
	}
	return tok, nil
}

// NewCredential builds the cached credential of a finished login.
func NewCredential(apiURL string, cfg LoginConfig, p Provider, tok *oauth2.Token) Credential {
	c := Credential{
		APIURL:           NormalizeAPIURL(apiURL),
		Issuer:           cfg.Issuer,
		ClientID:         cfg.ClientID,
		Scope:            strings.Join(cfg.Scopes(), " "),
		CAFile:           cfg.CAFile,
		TokenEndpoint:    p.TokenEndpoint,
		UserinfoEndpoint: p.UserinfoEndpoint,
	}
	c.setToken(tok)
	return c
}

// NewLogin returns an Authenticator that sends the cached login token and
// refreshes it with the refresh token when it expires. Refreshed tokens are
// written back to store. hc is used for the token endpoint (nil: default).
func NewLogin(store *Store, cred Credential, hc *http.Client) *Authenticator {
	if hc == nil {
		hc = &http.Client{Timeout: tokenFetchTimeout}
	}
	oc := oauthConfig(cred.ClientID, cred.TokenEndpoint, "", splitScopes(cred.Scope))
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, hc)
	return &Authenticator{
		mode: ModeLogin,
		src:  &persistingSource{src: oc.TokenSource(ctx, cred.Token()), store: store, cred: cred},
	}
}

// persistingSource writes tokens back to the store whenever the access token
// changed (i.e. after a refresh).
type persistingSource struct {
	mu    sync.Mutex
	src   oauth2.TokenSource
	store *Store
	cred  Credential
}

func (s *persistingSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, err := s.src.Token()
	if err != nil {
		return nil, &LoginExpiredError{APIURL: s.cred.APIURL, Err: err}
	}
	if tok.AccessToken != s.cred.AccessToken {
		s.cred.setToken(tok)
		if s.store != nil {
			if err := s.store.Put(s.cred); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save the refreshed login: %v\n", err)
			}
		}
	}
	return tok, nil
}

// LoginExpiredError means the cached login can no longer produce a token
// (refresh token expired or revoked, or none was issued).
type LoginExpiredError struct {
	APIURL string
	Err    error
}

func (e *LoginExpiredError) Error() string {
	return fmt.Sprintf("login for %s expired or was revoked, run `claims login --api-url %s` again: %s",
		e.APIURL, e.APIURL, refreshFailure(e.Err))
}

func (e *LoginExpiredError) Unwrap() error { return e.Err }

// refreshFailure names why a refresh failed without echoing request data.
func refreshFailure(err error) string {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		if re.ErrorCode != "" {
			return re.ErrorCode
		}
		if re.Response != nil {
			return fmt.Sprintf("token endpoint returned HTTP %d", re.Response.StatusCode)
		}
	}
	return err.Error()
}
