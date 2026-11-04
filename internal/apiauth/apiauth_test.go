package apiauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stuttgart-things/claims/internal/apiauth"
)

// headerRecorder is an API stand-in that records the Authorization header.
func headerRecorder(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// fakeTokenEndpoint serves OAuth2 client-credentials tokens and counts calls.
func fakeTokenEndpoint(t *testing.T, expiresIn int, wantScope string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "client_credentials" {
			t.Errorf("grant_type = %q, want client_credentials", got)
		}
		id, secret, ok := r.BasicAuth()
		if !ok {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		if id != "claims-ci" || secret != "s3cr3t" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		if got := r.PostForm.Get("scope"); got != wantScope {
			t.Errorf("scope = %q, want %q", got, wantScope)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "cc-token-" + string(rune('0'+n)),
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func doGet(t *testing.T, c *http.Client, url string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_ = resp.Body.Close()
}

func TestFromEnvAndMergePrecedence(t *testing.T) {
	t.Setenv(apiauth.EnvToken, "env-token")
	t.Setenv(apiauth.EnvOIDCTokenURL, "https://idp.example/token")
	t.Setenv(apiauth.EnvOIDCClientID, "env-id")
	t.Setenv(apiauth.EnvOIDCClientSecret, "env-secret")
	t.Setenv(apiauth.EnvOIDCScope, "openid")

	flags := apiauth.Config{Token: "flag-token", OIDCClientID: "flag-id"}
	got := flags.Merge(apiauth.FromEnv())
	want := apiauth.Config{
		Token:            "flag-token",
		OIDCTokenURL:     "https://idp.example/token",
		OIDCClientID:     "flag-id",
		OIDCClientSecret: "env-secret",
		OIDCScope:        "openid",
	}
	if got != want {
		t.Errorf("Merge = %+v, want %+v", got, want)
	}
}

func TestMode(t *testing.T) {
	tests := []struct {
		name string
		cfg  apiauth.Config
		want apiauth.Mode
	}{
		{"empty", apiauth.Config{}, apiauth.ModeNone},
		{"scope only", apiauth.Config{OIDCScope: "openid"}, apiauth.ModeNone},
		{"static", apiauth.Config{Token: "t"}, apiauth.ModeStaticToken},
		{"client credentials", apiauth.Config{OIDCTokenURL: "u", OIDCClientID: "i", OIDCClientSecret: "s"}, apiauth.ModeClientCredentials},
		{"static wins", apiauth.Config{Token: "t", OIDCTokenURL: "u", OIDCClientID: "i", OIDCClientSecret: "s"}, apiauth.ModeStaticToken},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.Mode(); got != tt.want {
				t.Errorf("Mode() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestValidateIncompleteClientCredentials(t *testing.T) {
	_, err := apiauth.New(apiauth.Config{OIDCClientID: "i", OIDCClientSecret: "super-secret"}, nil)
	if err == nil {
		t.Fatal("expected error for missing token URL")
	}
	if !strings.Contains(err.Error(), apiauth.EnvOIDCTokenURL) {
		t.Errorf("error should name %s: %v", apiauth.EnvOIDCTokenURL, err)
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Errorf("error leaks the client secret: %v", err)
	}
}

func TestNoAuthByDefault(t *testing.T) {
	auth, err := apiauth.New(apiauth.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth != nil {
		t.Fatalf("expected nil authenticator, got mode %s", auth.Mode())
	}
	srv, seen := headerRecorder(t)
	doGet(t, auth.HTTPClient(5*time.Second), srv.URL)
	if (*seen)[0] != "" {
		t.Errorf("Authorization header sent without config: %q", (*seen)[0])
	}
}

func TestStaticToken(t *testing.T) {
	auth, err := apiauth.New(apiauth.Config{
		Token:        "static-abc",
		OIDCTokenURL: "http://127.0.0.1:1/unused", OIDCClientID: "i", OIDCClientSecret: "s",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv, seen := headerRecorder(t)
	c := auth.HTTPClient(5 * time.Second)
	doGet(t, c, srv.URL)
	doGet(t, c, srv.URL+"/other")
	for i, h := range *seen {
		if h != "Bearer static-abc" {
			t.Errorf("request %d: Authorization = %q", i, h)
		}
	}
}

func TestWrapDoesNotMutateRequest(t *testing.T) {
	auth, _ := apiauth.New(apiauth.Config{Token: "x"}, nil)
	srv, _ := headerRecorder(t)
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := auth.Wrap(nil).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if req.Header.Get("Authorization") != "" {
		t.Error("RoundTrip modified the caller's request")
	}
}

func TestClientCredentialsCachesToken(t *testing.T) {
	idp, calls := fakeTokenEndpoint(t, 3600, "openid urn:zitadel:iam:org:project:id:123:aud")
	auth, err := apiauth.New(apiauth.Config{
		OIDCTokenURL:     idp.URL,
		OIDCClientID:     "claims-ci",
		OIDCClientSecret: "s3cr3t",
		OIDCScope:        "openid, urn:zitadel:iam:org:project:id:123:aud",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Mode() != apiauth.ModeClientCredentials {
		t.Fatalf("mode = %s", auth.Mode())
	}

	// Two "endpoints" share one authenticator, as with several CLAIM_API_URLs.
	api1, seen1 := headerRecorder(t)
	api2, seen2 := headerRecorder(t)
	c := auth.HTTPClient(5 * time.Second)
	doGet(t, c, api1.URL)
	doGet(t, c, api1.URL)
	doGet(t, auth.HTTPClient(5*time.Second), api2.URL)

	if n := atomic.LoadInt32(calls); n != 1 {
		t.Errorf("token endpoint called %d times, want 1 (cached)", n)
	}
	for _, h := range append(*seen1, *seen2...) {
		if h != "Bearer cc-token-1" {
			t.Errorf("Authorization = %q, want Bearer cc-token-1", h)
		}
	}
}

func TestClientCredentialsRefreshesExpiredToken(t *testing.T) {
	// expires_in below oauth2's expiry delta: every use needs a new token.
	idp, calls := fakeTokenEndpoint(t, 1, "")
	auth, err := apiauth.New(apiauth.Config{
		OIDCTokenURL: idp.URL, OIDCClientID: "claims-ci", OIDCClientSecret: "s3cr3t",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	api, seen := headerRecorder(t)
	c := auth.HTTPClient(5 * time.Second)
	doGet(t, c, api.URL)
	doGet(t, c, api.URL)
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Errorf("token endpoint called %d times, want 2", n)
	}
	if (*seen)[1] != "Bearer cc-token-2" {
		t.Errorf("second request Authorization = %q", (*seen)[1])
	}
}

func TestClientCredentialsTokenEndpointFailure(t *testing.T) {
	idp, _ := fakeTokenEndpoint(t, 3600, "")
	auth, err := apiauth.New(apiauth.Config{
		OIDCTokenURL: idp.URL, OIDCClientID: "claims-ci", OIDCClientSecret: "wrong-secret",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	api, seen := headerRecorder(t)
	_, err = auth.HTTPClient(5 * time.Second).Get(api.URL)
	if err == nil {
		t.Fatal("expected error when token endpoint rejects the client")
	}
	if len(*seen) != 0 {
		t.Error("API was called without a token")
	}
	if !strings.Contains(err.Error(), apiauth.EnvOIDCTokenURL) {
		t.Errorf("error should name %s: %v", apiauth.EnvOIDCTokenURL, err)
	}
	if strings.Contains(err.Error(), "wrong-secret") {
		t.Errorf("error leaks the client secret: %v", err)
	}
}

func TestStatusHint(t *testing.T) {
	h401 := apiauth.StatusHint(http.StatusUnauthorized)
	for _, v := range []string{"claims login", apiauth.EnvToken, "--api-token", apiauth.EnvOIDCTokenURL, apiauth.EnvOIDCClientID, apiauth.EnvOIDCClientSecret, apiauth.EnvOIDCScope} {
		if !strings.Contains(h401, v) {
			t.Errorf("401 hint missing %s: %s", v, h401)
		}
	}
	h403 := apiauth.StatusHint(http.StatusForbidden)
	if !strings.Contains(h403, "role") || !strings.Contains(h403, "claims whoami") {
		t.Errorf("403 hint: %s", h403)
	}
	if apiauth.StatusHint(http.StatusInternalServerError) != "" {
		t.Error("expected no hint for 500")
	}
}
