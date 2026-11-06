package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/stuttgart-things/claims/internal/apiauth"
)

var (
	loginAPIURL string
	loginFlags  apiauth.LoginConfig
	logoutAll   bool
)

var loginCmd = &cobra.Command{
	Use:     "login",
	GroupID: groupAuth,
	Short:   "Log in to claim-machinery-api with ZITADEL (device flow)",
	Long: `Log in to a claim-machinery-api endpoint as yourself, with the OAuth2
device authorization flow against the endpoint's ZITADEL.

claims prints a URL and a code; approve the login in the browser and the
tokens are cached per API URL (~/.config/claims/credentials.json, or
$` + apiauth.EnvCredentialsFile + `). render and encrypt send the cached token
to that API URL and refresh it when it expires.

Issuer and client ID come from --issuer/--client-id, $` + apiauth.EnvLoginIssuer + `/
$` + apiauth.EnvLoginClientID + `, or the previous login for the same API URL.

A token given with --api-token/$` + apiauth.EnvToken + ` or OIDC client credentials
($CLAIM_API_OIDC_*) wins over the cached login.`,
	Example: `  claims login --api-url https://claims.example.com/claims \
    --issuer https://zitadel.example.com --client-id 123456789@claims
  claims whoami --api-url https://claims.example.com/claims`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runLoginCmd(cmd.Context(), os.Stdout))
	},
}

var logoutCmd = &cobra.Command{
	Use:     "logout",
	GroupID: groupAuth,
	Short:   "Forget the cached login for an API URL",
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runLogoutCmd(os.Stdout))
	},
}

var whoamiCmd = &cobra.Command{
	Use:     "whoami",
	GroupID: groupAuth,
	Short:   "Show the identity and roles claims sends to claim-machinery-api",
	Long: `Show who claims authenticates as towards an API URL: the user, the
roles and groups in the token, and the user reference claim-machinery-api
stamps into requested_by. Uses the same precedence as render: --api-token /
$` + apiauth.EnvToken + `, then OIDC client credentials, then the cached login.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runWhoamiCmd(cmd.Context(), os.Stdout))
	},
}

func init() {
	for _, c := range []*cobra.Command{loginCmd, logoutCmd, whoamiCmd} {
		c.Flags().StringVarP(&loginAPIURL, "api-url", "a", "", "API URL the login is for (default: $CLAIM_API_URL or http://localhost:8080)")
	}
	f := loginCmd.Flags()
	f.StringVar(&loginFlags.Issuer, "issuer", "", "OIDC issuer, e.g. the lab ZITADEL (or $"+apiauth.EnvLoginIssuer+")")
	f.StringVar(&loginFlags.ClientID, "client-id", "", "Client ID of the native ZITADEL app (or $"+apiauth.EnvLoginClientID+")")
	f.StringVar(&loginFlags.Scope, "scope", "", "Scopes, space- or comma-separated (or $"+apiauth.EnvLoginScope+"; default \""+apiauth.DefaultLoginScope+"\")")
	f.StringVar(&loginFlags.CAFile, "ca-file", "", "Extra PEM roots for the issuer (or $"+apiauth.EnvLoginCAFile+")")
	logoutCmd.Flags().BoolVar(&logoutAll, "all", false, "Forget the logins for all API URLs")
	addAPIAuthFlags(whoamiCmd)

	rootCmd.AddCommand(loginCmd, logoutCmd, whoamiCmd)
}

func exitOn(err error) {
	if err != nil {
		fmt.Println(errorStyle.Render(err.Error()))
		os.Exit(1)
	}
}

// singleAPIURL resolves the API URL for login/logout/whoami: the flag, else
// $CLAIM_API_URL (which must then name exactly one endpoint), else the
// render default.
func singleAPIURL(flag string) (string, error) {
	if flag != "" {
		return apiauth.NormalizeAPIURL(flag), nil
	}
	env := os.Getenv("CLAIM_API_URL")
	if env == "" {
		return "http://localhost:8080", nil
	}
	urls := splitAPIURLs(env)
	if len(urls) > 1 {
		return "", fmt.Errorf("CLAIM_API_URL lists %d endpoints (%s); pick one with --api-url", len(urls), strings.Join(urls, ", "))
	}
	return apiauth.NormalizeAPIURL(urls[0]), nil
}

func runLoginCmd(ctx context.Context, out io.Writer) error {
	apiURL, err := singleAPIURL(loginAPIURL)
	if err != nil {
		return err
	}
	store, err := apiauth.DefaultStore()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	return runLogin(ctx, out, apiURL, loginFlags, store)
}

// runLogin performs the device flow for apiURL and caches the result.
func runLogin(ctx context.Context, out io.Writer, apiURL string, flags apiauth.LoginConfig, store *apiauth.Store) error {
	cfg := flags.Merge(apiauth.LoginConfigFromEnv())
	if prev, ok, err := store.Get(apiURL); err == nil && ok {
		cfg = cfg.Merge(prev.LoginConfig())
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	hc, err := apiauth.LoginHTTPClient(cfg.CAFile)
	if err != nil {
		return err
	}
	p, err := apiauth.Discover(ctx, hc, cfg.Issuer)
	if err != nil {
		return err
	}
	tok, err := apiauth.DeviceLogin(ctx, hc, p, cfg, func(da *oauth2.DeviceAuthResponse) {
		_, _ = fmt.Fprintf(out, "Logging in to %s via %s\n\n", apiURL, cfg.Issuer)
		_, _ = fmt.Fprintf(out, "  Open:  %s\n  Code:  %s\n", da.VerificationURI, da.UserCode)
		if da.VerificationURIComplete != "" {
			_, _ = fmt.Fprintf(out, "  or open %s\n", da.VerificationURIComplete)
		}
		_, _ = fmt.Fprintln(out, "\nWaiting for approval (Ctrl-C to abort) ...")
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return errors.New("login aborted")
		}
		return err
	}
	cred := apiauth.NewCredential(apiURL, cfg, p, tok)
	if err := store.Put(cred); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "\nLogged in. Token cached in %s\n", store.Path)
	if tok.RefreshToken == "" {
		_, _ = fmt.Fprintf(out, "Note: no refresh token was issued (scope offline_access missing?); the login ends %s.\n",
			tok.Expiry.Local().Format(time.RFC1123))
	}
	_, _ = fmt.Fprintln(out)
	printIdentity(ctx, out, apiauth.ModeLogin, tok.AccessToken, &cred, hc)
	return nil
}

func runLogoutCmd(out io.Writer) error {
	store, err := apiauth.DefaultStore()
	if err != nil {
		return err
	}
	if logoutAll {
		urls, err := store.URLs()
		if err != nil {
			return err
		}
		for _, u := range urls {
			if _, err := store.Delete(u); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Logged out of %s\n", u)
		}
		if len(urls) == 0 {
			_, _ = fmt.Fprintln(out, "No cached logins.")
		}
		return nil
	}
	apiURL, err := singleAPIURL(loginAPIURL)
	if err != nil {
		return err
	}
	return runLogout(out, apiURL, store)
}

func runLogout(out io.Writer, apiURL string, store *apiauth.Store) error {
	removed, err := store.Delete(apiURL)
	if err != nil {
		return err
	}
	if !removed {
		_, _ = fmt.Fprintf(out, "Not logged in to %s.\n", apiURL)
		return nil
	}
	_, _ = fmt.Fprintf(out, "Logged out of %s.\n", apiURL)
	return nil
}

func runWhoamiCmd(ctx context.Context, out io.Writer) error {
	apiURL, err := singleAPIURL(loginAPIURL)
	if err != nil {
		return err
	}
	store, err := apiauth.DefaultStore()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return runWhoami(ctx, out, apiURL, apiAuthFlags.Merge(apiauth.FromEnv()), store)
}

// runWhoami shows the identity behind the token claims would send to apiURL.
func runWhoami(ctx context.Context, out io.Writer, apiURL string, explicit apiauth.Config, store *apiauth.Store) error {
	auth, err := apiauth.Resolve(explicit, apiURL, store, warnf)
	if err != nil {
		return err
	}
	if auth == nil {
		return fmt.Errorf("no credentials for %s: run `claims login --api-url %s` (or set %s)", apiURL, apiURL, apiauth.EnvToken)
	}
	tok, err := auth.BearerToken()
	if err != nil {
		return err
	}
	var cred *apiauth.Credential
	var hc *http.Client
	if auth.Mode() == apiauth.ModeLogin {
		if c, ok, _ := store.Get(apiURL); ok {
			cred = &c
			hc, _ = apiauth.LoginHTTPClient(c.CAFile)
		}
	}
	_, _ = fmt.Fprintf(out, "API:        %s\n", apiURL)
	printIdentity(ctx, out, auth.Mode(), tok, cred, hc)
	return nil
}

// printIdentity prints what the token says about its holder. The token
// itself is never printed.
func printIdentity(ctx context.Context, out io.Writer, mode apiauth.Mode, token string, cred *apiauth.Credential, hc *http.Client) {
	_, _ = fmt.Fprintf(out, "Auth:       %s\n", mode)
	if cred != nil {
		_, _ = fmt.Fprintf(out, "Issuer:     %s\n", cred.Issuer)
	}
	claims, isJWT := apiauth.JWTClaims(token)
	if !isJWT {
		_, _ = fmt.Fprintln(out, "Warning:    the access token is not a JWT; claim-machinery-api only accepts JWTs\n"+
			"            (ZITADEL: set the application's Auth Token Type to JWT)")
		if cred == nil || cred.UserinfoEndpoint == "" || hc == nil {
			return
		}
		uc, err := apiauth.Userinfo(ctx, hc, cred.UserinfoEndpoint, token)
		if err != nil {
			_, _ = fmt.Fprintf(out, "            %v\n", err)
			return
		}
		claims = uc
	}
	id := apiauth.IdentityFromClaims(claims)
	row := func(k, v string) {
		if v != "" {
			_, _ = fmt.Fprintf(out, "%-11s %s\n", k+":", v)
		}
	}
	row("User", id.Username)
	row("Name", id.Name)
	row("Email", id.Email)
	row("Subject", id.Subject)
	row("User ref", id.UserRef())
	row("Roles", orNone(id.Roles))
	if len(id.Groups) > 0 {
		row("Groups", strings.Join(id.Groups, ", "))
	}
	row("Audience", strings.Join(id.Audience, ", "))
	if !id.Expiry.IsZero() {
		state := "valid"
		if time.Now().After(id.Expiry) {
			state = "expired"
		}
		row("Expires", fmt.Sprintf("%s (%s)", id.Expiry.Local().Format(time.RFC1123), state))
	}
}

func orNone(v []string) string {
	if len(v) == 0 {
		return "(none in the token)"
	}
	return strings.Join(v, ", ")
}

func warnf(msg string) {
	_, _ = fmt.Fprintln(os.Stderr, "warning: "+msg)
}
