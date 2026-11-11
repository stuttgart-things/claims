package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stuttgart-things/claims/internal/apiauth"
)

func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "none"}) + "." + enc(claims) + ".sig"
}

// deviceIdP approves the device code on the first poll.
func deviceIdP(t *testing.T, accessToken string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                        srv.URL,
			"token_endpoint":                srv.URL + "/token",
			"device_authorization_endpoint": srv.URL + "/device_authorization",
		})
	})
	mux.HandleFunc("/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code": "dc", "user_code": "WXYZ-1234",
			"verification_uri": srv.URL + "/device", "expires_in": 60, "interval": 1,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken, "token_type": "Bearer", "expires_in": 3600, "refresh_token": "rt-secret",
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// templateAPI records the Authorization header of template list calls.
func templateAPI(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ClaimTemplateList","items":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func clearAuthEnv(t *testing.T) {
	for _, k := range []string{
		apiauth.EnvToken, apiauth.EnvOIDCTokenURL, apiauth.EnvOIDCClientID, apiauth.EnvOIDCClientSecret, apiauth.EnvOIDCScope,
		apiauth.EnvLoginIssuer, apiauth.EnvLoginClientID, apiauth.EnvLoginScope, apiauth.EnvLoginCAFile, "CLAIM_API_URL",
	} {
		t.Setenv(k, "")
	}
}

func TestLoginWhoamiLogout(t *testing.T) {
	clearAuthEnv(t)
	credFile := filepath.Join(t.TempDir(), "credentials.json")
	t.Setenv(apiauth.EnvCredentialsFile, credFile)
	store := &apiauth.Store{Path: credFile}

	jwt := unsignedJWT(t, map[string]any{
		"sub": "42", "preferred_username": "jane@example.com", "aud": []string{"proj"}, "exp": 1893456000,
		"urn:zitadel:iam:org:project:roles": map[string]any{"backstage-developer": map[string]string{}},
	})
	idp := deviceIdP(t, jwt)
	api, seen := templateAPI(t)

	// login
	var out bytes.Buffer
	if err := runLogin(context.Background(), &out, api.URL, apiauth.LoginConfig{Issuer: idp.URL, ClientID: "claims-cli"}, store); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"WXYZ-1234", idp.URL + "/device", "Logged in", "user:default/jane", "backstage-developer"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("login output lacks %q:\n%s", want, out.String())
		}
	}
	for _, secret := range []string{jwt, "rt-secret"} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("login output leaks a token:\n%s", out.String())
		}
	}

	// render/encrypt clients pick the login up for that URL ...
	if _, err := newAPIClient(api.URL+"/", nil).FetchTemplates(); err != nil {
		t.Fatal(err)
	}
	// ... but not for another one ...
	other, otherSeen := templateAPI(t)
	if _, err := newAPIClient(other.URL, nil).FetchTemplates(); err != nil {
		t.Fatal(err)
	}
	// ... and an explicit token wins.
	static, _ := apiauth.New(apiauth.Config{Token: "static"}, nil)
	if _, err := newAPIClient(api.URL, static).FetchTemplates(); err != nil {
		t.Fatal(err)
	}
	if got := seen(); len(got) != 2 || got[0] != "Bearer "+jwt || got[1] != "Bearer static" {
		t.Errorf("API saw %v", got)
	}
	if got := otherSeen(); len(got) != 1 || got[0] != "" {
		t.Errorf("other API saw %v", got)
	}

	// whoami
	out.Reset()
	if err := runWhoami(context.Background(), &out, api.URL, apiauth.Config{}, store); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Auth:       login", "jane@example.com", "user:default/jane", "backstage-developer", "proj"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("whoami output lacks %q:\n%s", want, out.String())
		}
	}

	// a second login reuses issuer and client ID of the first
	out.Reset()
	if err := runLogin(context.Background(), &out, api.URL, apiauth.LoginConfig{}, store); err != nil {
		t.Fatalf("re-login without flags: %v", err)
	}

	// logout
	out.Reset()
	if err := runLogout(&out, api.URL, store); err != nil || !strings.Contains(out.String(), "Logged out") {
		t.Fatalf("logout: %v %s", err, out.String())
	}
	err := runWhoami(context.Background(), &out, api.URL, apiauth.Config{}, store)
	if err == nil || !strings.Contains(err.Error(), "claims login --api-url "+api.URL) {
		t.Errorf("whoami after logout: %v", err)
	}
}

func TestWhoamiOpaqueToken(t *testing.T) {
	clearAuthEnv(t)
	store := &apiauth.Store{Path: filepath.Join(t.TempDir(), "c.json")}
	var out bytes.Buffer
	if err := runWhoami(context.Background(), &out, "https://api", apiauth.Config{Token: "opaque-secret"}, store); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not a JWT") || !strings.Contains(out.String(), "static-token") {
		t.Errorf("output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "opaque-secret") {
		t.Error("whoami printed the token")
	}
}

func TestLoginNeedsIssuerAndClient(t *testing.T) {
	clearAuthEnv(t)
	store := &apiauth.Store{Path: filepath.Join(t.TempDir(), "c.json")}
	err := runLogin(context.Background(), &bytes.Buffer{}, "https://api", apiauth.LoginConfig{}, store)
	if err == nil || !strings.Contains(err.Error(), apiauth.EnvLoginIssuer) {
		t.Errorf("err = %v", err)
	}
}

func TestSingleAPIURL(t *testing.T) {
	isolateEndpoints(t)
	// No localhost default any more (claims#116).
	if _, err := singleAPIURL(""); !errors.Is(err, errNoEndpoint) {
		t.Errorf("nothing set: %v", err)
	}
	if u, _ := singleAPIURL("https://x/claims/"); u.URL != "https://x/claims" {
		t.Errorf("flag = %s", u.URL)
	}
	t.Setenv("CLAIM_API_URL", "https://a:8080:https://b")
	if _, err := singleAPIURL(""); err == nil || !strings.Contains(err.Error(), "--api-url") {
		t.Errorf("multiple endpoints: %v", err)
	}
	t.Setenv("CLAIM_API_URL", "https://a/claims")
	if u, _ := singleAPIURL(""); u.URL != "https://a/claims" {
		t.Errorf("env = %s", u.URL)
	}
}
