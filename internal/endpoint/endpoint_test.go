package endpoint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/endpoint"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	c, err := endpoint.Load(path)
	if err != nil || len(c.Endpoints) != 0 {
		t.Fatalf("missing file: %v %v", c, err)
	}
	if err := c.Add("labda", endpoint.Endpoint{URL: "https://api.labda/", Issuer: "https://idp", ClientID: "1@claims"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("labul", endpoint.Endpoint{URL: "https://api.labul"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Use("labda"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	got, err := endpoint.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Current != "labda" || got.Endpoints["labda"].URL != "https://api.labda" || got.Endpoints["labda"].ClientID != "1@claims" {
		t.Errorf("got %+v", got)
	}
	if n, _, ok := got.ByURL("https://api.labul/"); !ok || n != "labul" {
		t.Errorf("ByURL = %q %v", n, ok)
	}
	if err := got.Remove("labda"); err != nil || got.Current != "" {
		t.Errorf("remove current: %v current=%q", err, got.Current)
	}
}

func TestValidation(t *testing.T) {
	c := &endpoint.Config{Endpoints: map[string]endpoint.Endpoint{}}
	for name, tc := range map[string]struct {
		name string
		e    endpoint.Endpoint
		want string
	}{
		"bad name":       {"a b", endpoint.Endpoint{URL: "https://x"}, "endpoint name"},
		"no scheme":      {"x", endpoint.Endpoint{URL: "claims.example.com"}, "http://"},
		"issuer w/o id":  {"x", endpoint.Endpoint{URL: "https://x", Issuer: "https://idp"}, "go together"},
		"client w/o iss": {"x", endpoint.Endpoint{URL: "https://x", ClientID: "1"}, "go together"},
	} {
		t.Run(name, func(t *testing.T) {
			err := c.Add(tc.name, tc.e)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if err := c.Use("nope"); err == nil || !strings.Contains(err.Error(), "none configured") {
		t.Errorf("use unknown: %v", err)
	}
	_ = c.Add("a", endpoint.Endpoint{URL: "https://a"})
	if _, err := c.Get("b"); err == nil || !strings.Contains(err.Error(), "configured: a") {
		t.Errorf("get unknown: %v", err)
	}
}

func TestDefaultPathEnv(t *testing.T) {
	t.Setenv(endpoint.EnvConfigFile, "/x/c.yaml")
	if p, _ := endpoint.DefaultPath(); p != "/x/c.yaml" {
		t.Errorf("path = %s", p)
	}
}
