package templates

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/apiauth"
)

func TestNewClient(t *testing.T) {
	client := NewClient("http://localhost:8080")

	if client.BaseURL != "http://localhost:8080" {
		t.Errorf("expected BaseURL to be http://localhost:8080, got %s", client.BaseURL)
	}

	if client.HTTPClient == nil {
		t.Error("expected HTTPClient to be initialized")
	}

	if client.HTTPClient.Timeout.Seconds() != 30 {
		t.Errorf("expected timeout to be 30s, got %v", client.HTTPClient.Timeout)
	}
}

func TestNewClientWithHTTPClient(t *testing.T) {
	customClient := &http.Client{}
	client := NewClientWithHTTPClient("http://example.com", customClient)

	if client.BaseURL != "http://example.com" {
		t.Errorf("expected BaseURL to be http://example.com, got %s", client.BaseURL)
	}

	if client.HTTPClient != customClient {
		t.Error("expected HTTPClient to be the custom client")
	}
}

func TestFetchTemplates(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/claim-templates" {
			t.Errorf("expected path /api/v1/claim-templates, got %s", r.URL.Path)
		}

		if r.Method != http.MethodGet {
			t.Errorf("expected GET method, got %s", r.Method)
		}

		response := ClaimTemplateList{
			APIVersion: "claims.sthings.io/v1",
			Kind:       "ClaimTemplateList",
			Items: []ClaimTemplate{
				{
					APIVersion: "claims.sthings.io/v1",
					Kind:       "ClaimTemplate",
					Metadata: ClaimTemplateMetadata{
						Name:        "test-template",
						Title:       "Test Template",
						Description: "A test template",
						Tags:        []string{"test"},
					},
					Spec: ClaimTemplateSpec{
						Type:   "kcl",
						Source: "ghcr.io/test/template",
						Tag:    "v1.0.0",
						Parameters: []Parameter{
							{
								Name:     "name",
								Title:    "Name",
								Type:     "string",
								Required: true,
							},
						},
					},
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	templates, err := client.FetchTemplates()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}

	if templates[0].Metadata.Name != "test-template" {
		t.Errorf("expected template name to be test-template, got %s", templates[0].Metadata.Name)
	}

	if templates[0].Metadata.Title != "Test Template" {
		t.Errorf("expected template title to be Test Template, got %s", templates[0].Metadata.Title)
	}

	if len(templates[0].Spec.Parameters) != 1 {
		t.Errorf("expected 1 parameter, got %d", len(templates[0].Spec.Parameters))
	}
}

func TestFetchTemplatesError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.FetchTemplates()

	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestRenderTemplate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/claim-templates/test-template/order" {
			t.Errorf("expected path /api/v1/claim-templates/test-template/order, got %s", r.URL.Path)
		}

		if r.Method != http.MethodPost {
			t.Errorf("expected POST method, got %s", r.Method)
		}

		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		var req OrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}

		if req.Parameters["name"] != "test-resource" {
			t.Errorf("expected parameter name to be test-resource, got %v", req.Parameters["name"])
		}

		response := OrderResponse{
			APIVersion: "claims.sthings.io/v1",
			Kind:       "OrderResponse",
			Metadata:   map[string]interface{}{"name": "test-resource"},
			Rendered:   "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-resource\n",
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	params := map[string]interface{}{
		"name": "test-resource",
	}

	result, err := client.RenderTemplate("test-template", params)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-resource\n"
	if result != expected {
		t.Errorf("expected rendered output %q, got %q", expected, result)
	}
}

func TestRenderTemplateError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid parameters"))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.RenderTemplate("test-template", map[string]interface{}{})

	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestRenderTemplateConnectionError(t *testing.T) {
	client := NewClient("http://localhost:99999")
	_, err := client.FetchTemplates()

	if err == nil {
		t.Error("expected connection error, got nil")
	}
}

func newOrderServer(t *testing.T, status int, seen *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Header.Get("Authorization"))
		if status != http.StatusOK {
			if status == http.StatusUnauthorized {
				w.Header().Set("WWW-Authenticate", `Bearer realm="claim-machinery-api"`)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"denied"}`))
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(ClaimTemplateList{})
		default:
			_ = json.NewEncoder(w).Encode(OrderResponse{Rendered: "kind: X"})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthHeaderAbsentByDefault(t *testing.T) {
	var seen []string
	srv := newOrderServer(t, http.StatusOK, &seen)
	for _, c := range []*Client{NewClient(srv.URL), NewClientWithAuth(srv.URL, nil)} {
		if _, err := c.FetchTemplates(); err != nil {
			t.Fatal(err)
		}
		if _, err := c.RenderTemplate("vm", map[string]interface{}{}); err != nil {
			t.Fatal(err)
		}
	}
	for i, h := range seen {
		if h != "" {
			t.Errorf("request %d: unexpected Authorization header %q", i, h)
		}
	}
}

func TestAuthHeaderWithToken(t *testing.T) {
	auth, err := apiauth.New(apiauth.Config{Token: "tok-123"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	srv := newOrderServer(t, http.StatusOK, &seen)
	c := NewClientWithAuth(srv.URL, auth)
	if c.HTTPClient.Timeout != defaultTimeout {
		t.Errorf("timeout = %v, want %v", c.HTTPClient.Timeout, defaultTimeout)
	}
	if _, err := c.FetchTemplates(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RenderTemplate("vm", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(seen))
	}
	for i, h := range seen {
		if h != "Bearer tok-123" {
			t.Errorf("request %d: Authorization = %q", i, h)
		}
	}
}

func TestAuthErrorMessages(t *testing.T) {
	tests := []struct {
		status int
		want   []string
	}{
		{http.StatusUnauthorized, []string{"API returned 401", `{"error":"denied"}`, "CLAIM_API_TOKEN", "--api-token", "CLAIM_API_OIDC_TOKEN_URL", "CLAIM_API_OIDC_CLIENT_ID", "CLAIM_API_OIDC_CLIENT_SECRET"}},
		{http.StatusForbidden, []string{"API returned 403", "role", "claims whoami"}},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			auth, _ := apiauth.New(apiauth.Config{Token: "very-secret-token"}, nil)
			var seen []string
			srv := newOrderServer(t, tt.status, &seen)
			c := NewClientWithAuth(srv.URL, auth)

			_, err := c.RenderTemplate("vm", map[string]interface{}{})
			if err == nil {
				t.Fatal("expected error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error missing %q:\n%s", w, err)
				}
			}
			if strings.Contains(err.Error(), "very-secret-token") {
				t.Errorf("error leaks the token:\n%s", err)
			}

			_, err = c.FetchTemplates()
			if err == nil || !strings.Contains(err.Error(), tt.want[2]) {
				t.Errorf("FetchTemplates error missing hint: %v", err)
			}
		})
	}
}

func TestNonAuthErrorHasNoHint(t *testing.T) {
	var seen []string
	srv := newOrderServer(t, http.StatusInternalServerError, &seen)
	_, err := NewClient(srv.URL).FetchTemplates()
	if err == nil || strings.Contains(err.Error(), "CLAIM_API_TOKEN") {
		t.Errorf("unexpected error: %v", err)
	}
}
