package apiauth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/stuttgart-things/claims/internal/apiauth"
)

// fakeJWT builds an unsigned JWT with the given claims (display tests only;
// the API verifies signatures, claims does not).
func fakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]string{"alg": "none", "typ": "JWT"}) + "." + enc(claims) + ".sig"
}

// fakeIdP is a minimal ZITADEL stand-in: discovery, device authorization,
// token endpoint (device code + refresh token grants), userinfo.
type fakeIdP struct {
	t   *testing.T
	srv *httptest.Server

	mu            sync.Mutex
	pending       int    // authorization_pending answers before the token
	refreshStatus int    // status for refresh grants (0 = 200)
	accessToken   string // next access token handed out
	refreshCalls  int
	deviceForm    map[string]string
	noDevice      bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	f := &fakeIdP{t: t, pending: 1, accessToken: "access-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		doc := map[string]string{
			"issuer":            f.srv.URL,
			"token_endpoint":    f.srv.URL + "/oauth/v2/token",
			"userinfo_endpoint": f.srv.URL + "/oidc/v1/userinfo",
		}
		f.mu.Lock()
		noDevice := f.noDevice
		f.mu.Unlock()
		if !noDevice {
			doc["device_authorization_endpoint"] = f.srv.URL + "/oauth/v2/device_authorization"
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/oauth/v2/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.deviceForm = map[string]string{"client_id": r.PostForm.Get("client_id"), "scope": r.PostForm.Get("scope")}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "dev-code-1",
			"user_code":                 "ABCD-EFGH",
			"verification_uri":          f.srv.URL + "/device",
			"verification_uri_complete": f.srv.URL + "/device?user_code=ABCD-EFGH",
			"expires_in":                300,
			"interval":                  1,
		})
	})
	mux.HandleFunc("/oauth/v2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if got := r.PostForm.Get("client_id"); got != "claims-cli" {
			t.Errorf("token endpoint: client_id = %q", got)
		}
		switch r.PostForm.Get("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			if r.PostForm.Get("device_code") != "dev-code-1" {
				t.Errorf("device_code = %q", r.PostForm.Get("device_code"))
			}
			if f.pending > 0 {
				f.pending--
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
		case "refresh_token":
			f.refreshCalls++
			if f.refreshStatus != 0 {
				w.WriteHeader(f.refreshStatus)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token expired"}`))
				return
			}
		default:
			t.Errorf("unexpected grant_type %q", r.PostForm.Get("grant_type"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  f.accessToken,
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "refresh-" + f.accessToken,
			"id_token":      "id-" + f.accessToken,
		})
	})
	mux.HandleFunc("/oidc/v1/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "1", "preferred_username": "jdoe@example.com"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) config() apiauth.LoginConfig {
	return apiauth.LoginConfig{Issuer: f.srv.URL, ClientID: "claims-cli"}
}

func tempStore(t *testing.T) *apiauth.Store {
	t.Helper()
	return &apiauth.Store{Path: filepath.Join(t.TempDir(), "claims", "credentials.json")}
}

func TestLoginConfigValidate(t *testing.T) {
	err := apiauth.LoginConfig{}.Validate()
	if err == nil || !strings.Contains(err.Error(), apiauth.EnvLoginIssuer) || !strings.Contains(err.Error(), apiauth.EnvLoginClientID) {
		t.Fatalf("Validate() = %v", err)
	}
	if err := (apiauth.LoginConfig{Issuer: "x", ClientID: "y"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(apiauth.LoginConfig{}.Scopes(), " "); got != apiauth.DefaultLoginScope {
		t.Errorf("default scopes = %q", got)
	}
	if got := (apiauth.LoginConfig{Scope: "openid,foo bar"}).Scopes(); len(got) != 3 {
		t.Errorf("scopes = %v", got)
	}
}

func TestDiscover(t *testing.T) {
	idp := newFakeIdP(t)
	p, err := apiauth.Discover(context.Background(), http.DefaultClient, idp.srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if p.TokenEndpoint != idp.srv.URL+"/oauth/v2/token" || p.DeviceAuthorizationEndpoint == "" || p.UserinfoEndpoint == "" {
		t.Errorf("provider = %+v", p)
	}

	idp.mu.Lock()
	idp.noDevice = true
	idp.mu.Unlock()
	if _, err := apiauth.Discover(context.Background(), http.DefaultClient, idp.srv.URL); err == nil || !strings.Contains(err.Error(), "device") {
		t.Errorf("want device-flow error, got %v", err)
	}
	if _, err := apiauth.Discover(context.Background(), http.DefaultClient, idp.srv.URL+"/nope"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("want 404 error, got %v", err)
	}
}

func TestDeviceLogin(t *testing.T) {
	idp := newFakeIdP(t)
	cfg := idp.config()
	cfg.Scope = "openid offline_access urn:zitadel:iam:org:projects:roles"
	p, err := apiauth.Discover(context.Background(), http.DefaultClient, cfg.Issuer)
	if err != nil {
		t.Fatal(err)
	}
	var shown *oauth2.DeviceAuthResponse
	tok, err := apiauth.DeviceLogin(context.Background(), http.DefaultClient, p, cfg, func(da *oauth2.DeviceAuthResponse) { shown = da })
	if err != nil {
		t.Fatal(err)
	}
	if shown == nil || shown.UserCode != "ABCD-EFGH" || !strings.HasSuffix(shown.VerificationURI, "/device") {
		t.Errorf("shown = %+v", shown)
	}
	if tok.AccessToken != "access-1" || tok.RefreshToken != "refresh-access-1" {
		t.Errorf("token = %+v", tok)
	}
	idp.mu.Lock()
	form := idp.deviceForm
	idp.mu.Unlock()
	if form["client_id"] != "claims-cli" || form["scope"] != cfg.Scope {
		t.Errorf("device request = %v", form)
	}

	cred := apiauth.NewCredential("https://api.example.com/claims/", cfg, p, tok)
	if cred.APIURL != "https://api.example.com/claims" || cred.IDToken != "id-access-1" || cred.TokenEndpoint != p.TokenEndpoint {
		t.Errorf("credential = %+v", cred)
	}
}

func TestDeviceLoginCanceled(t *testing.T) {
	idp := newFakeIdP(t)
	idp.pending = 100
	p, err := apiauth.Discover(context.Background(), http.DefaultClient, idp.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = apiauth.DeviceLogin(ctx, http.DefaultClient, p, idp.config(), func(*oauth2.DeviceAuthResponse) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestStore(t *testing.T) {
	s := tempStore(t)
	if _, ok, err := s.Get("https://a"); ok || err != nil {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	a := apiauth.Credential{APIURL: "https://a/claims/", Issuer: "https://idp-a", AccessToken: "ta"}
	b := apiauth.Credential{APIURL: "https://b", Issuer: "https://idp-b", AccessToken: "tb"}
	for _, c := range []apiauth.Credential{a, b} {
		if err := s.Put(c); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.Get(" https://a/claims")
	if err != nil || !ok || got.Issuer != "https://idp-a" {
		t.Fatalf("Get = %+v %v %v", got, ok, err)
	}
	fi, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("credentials file mode = %v, want 0600", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(s.Path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("credentials dir mode = %v, want 0700", di.Mode().Perm())
	}
	urls, _ := s.URLs()
	if strings.Join(urls, ",") != "https://a/claims,https://b" {
		t.Errorf("URLs = %v", urls)
	}
	if removed, err := s.Delete("https://a/claims"); !removed || err != nil {
		t.Fatalf("Delete = %v %v", removed, err)
	}
	if removed, _ := s.Delete("https://a/claims"); removed {
		t.Error("second Delete reported a removal")
	}
	if _, ok, _ := s.Get("https://b"); !ok {
		t.Error("other login lost")
	}
}

func TestStoreCorrupt(t *testing.T) {
	s := tempStore(t)
	_ = os.MkdirAll(filepath.Dir(s.Path), 0o700)
	_ = os.WriteFile(s.Path, []byte("{nope"), 0o600)
	if _, _, err := s.Get("https://a"); err == nil {
		t.Fatal("want parse error")
	}
	var warned string
	auth, err := apiauth.Resolve(apiauth.Config{}, "https://a", s, func(m string) { warned = m })
	if auth != nil || err != nil || !strings.Contains(warned, "credentials.json") {
		t.Errorf("Resolve on corrupt store = %v, %v, warn %q", auth, err, warned)
	}
}

// storeExpiredLogin caches a login whose access token has expired.
func storeExpiredLogin(t *testing.T, idp *fakeIdP, apiURL string) *apiauth.Store {
	t.Helper()
	s := tempStore(t)
	err := s.Put(apiauth.Credential{
		APIURL:        apiURL,
		Issuer:        idp.srv.URL,
		ClientID:      "claims-cli",
		Scope:         "openid offline_access",
		TokenEndpoint: idp.srv.URL + "/oauth/v2/token",
		AccessToken:   "old-access",
		RefreshToken:  "old-refresh-secret",
		TokenType:     "Bearer",
		Expiry:        time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoginRefreshOnExpiry(t *testing.T) {
	idp := newFakeIdP(t)
	idp.accessToken = "new-access"
	api, seen := headerRecorder(t)
	store := storeExpiredLogin(t, idp, api.URL)

	auth, err := apiauth.Resolve(apiauth.Config{}, api.URL, store, func(m string) { t.Errorf("warn: %s", m) })
	if err != nil || auth.Mode() != apiauth.ModeLogin {
		t.Fatalf("Resolve = %v, %v", auth.Mode(), err)
	}
	c := auth.HTTPClient(5 * time.Second)
	for i := 0; i < 2; i++ {
		resp, err := c.Get(api.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	for _, h := range *seen {
		if h != "Bearer new-access" {
			t.Errorf("Authorization = %q, want the refreshed token", h)
		}
	}
	idp.mu.Lock()
	calls := idp.refreshCalls
	idp.mu.Unlock()
	if calls != 1 {
		t.Errorf("refresh calls = %d, want 1", calls)
	}
	cred, _, _ := store.Get(api.URL)
	if cred.AccessToken != "new-access" || cred.RefreshToken != "refresh-new-access" || !cred.Expiry.After(time.Now()) {
		t.Errorf("refreshed login not saved: %+v", cred)
	}
}

func TestLoginRefreshFailure(t *testing.T) {
	idp := newFakeIdP(t)
	idp.refreshStatus = http.StatusBadRequest
	api, seen := headerRecorder(t)
	store := storeExpiredLogin(t, idp, api.URL)

	auth, _ := apiauth.Resolve(apiauth.Config{}, api.URL, store, func(string) {})
	_, err := auth.HTTPClient(5 * time.Second).Get(api.URL)
	if err == nil {
		t.Fatal("want error")
	}
	var expired *apiauth.LoginExpiredError
	if !errors.As(err, &expired) {
		t.Errorf("want LoginExpiredError, got %T", err)
	}
	msg := err.Error()
	for _, want := range []string{"claims login --api-url " + api.URL, "invalid_grant"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	for _, secret := range []string{"old-refresh-secret", "old-access"} {
		if strings.Contains(msg, secret) {
			t.Errorf("error leaks %s: %s", secret, msg)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("request reached the API: %v", *seen)
	}
}

func TestResolvePrecedence(t *testing.T) {
	idp := newFakeIdP(t)
	store := storeExpiredLogin(t, idp, "https://api")
	noWarn := func(m string) { t.Errorf("warn: %s", m) }

	cases := []struct {
		name   string
		cfg    apiauth.Config
		apiURL string
		want   apiauth.Mode
	}{
		{"static token wins over login", apiauth.Config{Token: "tok"}, "https://api", apiauth.ModeStaticToken},
		{"client credentials win over login", apiauth.Config{OIDCTokenURL: "https://idp/token", OIDCClientID: "id", OIDCClientSecret: "s"}, "https://api", apiauth.ModeClientCredentials},
		{"cached login for the URL", apiauth.Config{}, "https://api/", apiauth.ModeLogin},
		{"no login for another URL", apiauth.Config{}, "https://other", apiauth.ModeNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := apiauth.Resolve(tc.cfg, tc.apiURL, store, noWarn)
			if err != nil {
				t.Fatal(err)
			}
			if auth.Mode() != tc.want {
				t.Errorf("mode = %s, want %s", auth.Mode(), tc.want)
			}
		})
	}
}

func TestIdentityFromClaims(t *testing.T) {
	tok := fakeJWT(t, map[string]any{
		"sub":                "3141",
		"preferred_username": "Jane.Doe@example.com",
		"email":              "jane.doe@example.com",
		"aud":                []string{"123456", "claims-cli"},
		"exp":                1893456000,
		"urn:zitadel:iam:org:project:roles": map[string]any{
			"backstage-developer": map[string]string{"1": "org"},
		},
		"urn:zitadel:iam:org:project:123456:roles": map[string]any{
			"backstage-team-ops":  map[string]string{"1": "org"},
			"backstage-developer": map[string]string{"1": "org"},
		},
		"groups": []string{"ops"},
	})
	claims, ok := apiauth.JWTClaims(tok)
	if !ok {
		t.Fatal("JWTClaims: not a JWT")
	}
	id := apiauth.IdentityFromClaims(claims)
	if got := strings.Join(id.Roles, ","); got != "backstage-developer,backstage-team-ops" {
		t.Errorf("roles = %s", got)
	}
	if id.UserRef() != "user:default/jane.doe" {
		t.Errorf("UserRef = %s", id.UserRef())
	}
	if strings.Join(id.Audience, ",") != "123456,claims-cli" || strings.Join(id.Groups, ",") != "ops" || id.Expiry.Unix() != 1893456000 {
		t.Errorf("identity = %+v", id)
	}
	if _, ok := apiauth.JWTClaims("opaque-token"); ok {
		t.Error("opaque token parsed as JWT")
	}
}

func TestUserinfo(t *testing.T) {
	idp := newFakeIdP(t)
	claims, err := apiauth.Userinfo(context.Background(), http.DefaultClient, idp.srv.URL+"/oidc/v1/userinfo", "access-1")
	if err != nil {
		t.Fatal(err)
	}
	if apiauth.IdentityFromClaims(claims).Username != "jdoe@example.com" {
		t.Errorf("claims = %v", claims)
	}
}

func TestLoginHTTPClientCAFile(t *testing.T) {
	if _, err := apiauth.LoginHTTPClient(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Error("want error for a missing CA file")
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	_ = os.WriteFile(bad, []byte("not a cert"), 0o600)
	if _, err := apiauth.LoginHTTPClient(bad); err == nil {
		t.Error("want error for a CA file without certificates")
	}
}
