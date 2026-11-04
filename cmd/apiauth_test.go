package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/apiauth"
)

func clearAPIAuthEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{apiauth.EnvToken, apiauth.EnvOIDCTokenURL, apiauth.EnvOIDCClientID, apiauth.EnvOIDCClientSecret, apiauth.EnvOIDCScope} {
		t.Setenv(k, "")
	}
	old := apiAuthFlags
	apiAuthFlags = apiauth.Config{}
	t.Cleanup(func() { apiAuthFlags = old })
}

func TestResolveAPIAuth(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		clearAPIAuthEnv(t)
		auth, err := resolveAPIAuth()
		if err != nil || auth != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", auth, err)
		}
	})
	t.Run("env token", func(t *testing.T) {
		clearAPIAuthEnv(t)
		t.Setenv(apiauth.EnvToken, "env-tok")
		assertBearer(t, "Bearer env-tok")
	})
	t.Run("flag wins over env", func(t *testing.T) {
		clearAPIAuthEnv(t)
		t.Setenv(apiauth.EnvToken, "env-tok")
		apiAuthFlags.Token = "flag-tok"
		assertBearer(t, "Bearer flag-tok")
	})
	t.Run("incomplete client credentials", func(t *testing.T) {
		clearAPIAuthEnv(t)
		t.Setenv(apiauth.EnvOIDCClientID, "id")
		if _, err := resolveAPIAuth(); err == nil || !strings.Contains(err.Error(), apiauth.EnvOIDCClientSecret) {
			t.Errorf("expected error naming missing vars, got %v", err)
		}
	})
}

func TestAPIAuthFlagsRegistered(t *testing.T) {
	for _, c := range []string{"render", "encrypt"} {
		cmd, _, err := rootCmd.Find([]string{c})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"api-token", "api-oidc-token-url", "api-oidc-client-id", "api-oidc-client-secret", "api-oidc-scope"} {
			if cmd.Flags().Lookup(f) == nil {
				t.Errorf("%s: flag --%s not registered", c, f)
			}
		}
	}
}

func assertBearer(t *testing.T, want string) {
	t.Helper()
	auth, err := resolveAPIAuth()
	if err != nil {
		t.Fatal(err)
	}
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	if _, err := newAPIClient(srv.URL, auth).FetchTemplates(); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
}
