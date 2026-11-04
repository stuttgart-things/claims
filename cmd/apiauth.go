package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/apiauth"
	"github.com/stuttgart-things/claims/internal/templates"
)

// apiAuthFlags holds the --api-token / --api-oidc-* flag values. Each command
// that talks to claim-machinery-api registers them via addAPIAuthFlags.
var apiAuthFlags apiauth.Config

// addAPIAuthFlags registers the optional bearer-token auth flags on c.
func addAPIAuthFlags(c *cobra.Command) {
	f := c.Flags()
	f.StringVar(&apiAuthFlags.Token, "api-token", "", "Bearer token for claim-machinery-api (or $"+apiauth.EnvToken+"); wins over OIDC client credentials")
	f.StringVar(&apiAuthFlags.OIDCTokenURL, "api-oidc-token-url", "", "OIDC token endpoint for client credentials (or $"+apiauth.EnvOIDCTokenURL+")")
	f.StringVar(&apiAuthFlags.OIDCClientID, "api-oidc-client-id", "", "OIDC client ID for client credentials (or $"+apiauth.EnvOIDCClientID+")")
	f.StringVar(&apiAuthFlags.OIDCClientSecret, "api-oidc-client-secret", "", "OIDC client secret for client credentials (or $"+apiauth.EnvOIDCClientSecret+")")
	f.StringVar(&apiAuthFlags.OIDCScope, "api-oidc-scope", "", "OIDC scope(s) for client credentials, space- or comma-separated (or $"+apiauth.EnvOIDCScope+")")
}

// resolveAPIAuth builds the authenticator from flags (preferred) and the
// CLAIM_API_* environment. It returns nil when auth is not configured.
func resolveAPIAuth() (*apiauth.Authenticator, error) {
	auth, err := apiauth.New(apiAuthFlags.Merge(apiauth.FromEnv()), nil)
	if err != nil {
		return nil, fmt.Errorf("API auth: %w", err)
	}
	return auth, nil
}

// newAPIClient returns a claim-machinery-api client for baseURL that sends the
// configured bearer token (if any). The same authenticator, and therefore the
// same token, is used for every endpoint in CLAIM_API_URL.
func newAPIClient(baseURL string, auth *apiauth.Authenticator) *templates.Client {
	return templates.NewClientWithAuth(baseURL, auth)
}
