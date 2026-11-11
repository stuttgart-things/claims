package cmd

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/apiauth"
	"github.com/stuttgart-things/claims/internal/endpoint"
)

// isolateEndpoints gives the test its own (empty) config and credentials
// file and clears every variable that picks the API.
func isolateEndpoints(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv(endpoint.EnvConfigFile, path)
	t.Setenv(apiauth.EnvCredentialsFile, filepath.Join(dir, "credentials.json"))
	t.Setenv("CLAIM_API_URL", "")
	t.Setenv(endpoint.EnvEndpoint, "")
	for _, k := range []string{apiauth.EnvToken, apiauth.EnvOIDCTokenURL, apiauth.EnvOIDCClientID, apiauth.EnvOIDCClientSecret,
		apiauth.EnvLoginIssuer, apiauth.EnvLoginClientID, apiauth.EnvLoginCAFile, apiauth.EnvLoginScope} {
		t.Setenv(k, "")
	}
	prevFlag, prevLogin, prevPick := endpointFlag, loginFlags, pickEndpoint
	endpointFlag, loginFlags = "", apiauth.LoginConfig{}
	pickEndpoint = func(*endpoint.Config) (string, error) {
		t.Error("picker shown")
		return "", errors.New("no picker in tests")
	}
	t.Cleanup(func() { endpointFlag, loginFlags, pickEndpoint = prevFlag, prevLogin, prevPick })
	return path
}

const (
	labdaURL = "https://claim-machinery-api.machinery.4sthings.tiab.ssc.sva.de"
	labulURL = "https://claim-machinery-api.machinery-labul.sthings-vsphere.labul.sva.de"
)

// writeEndpoints configures labda (with login settings, current) and labul.
func writeEndpoints(t *testing.T, path string) {
	t.Helper()
	c := &endpoint.Config{Endpoints: map[string]endpoint.Endpoint{}}
	_ = c.Add("labda", endpoint.Endpoint{URL: labdaURL, Issuer: "https://zitadel.example", ClientID: "42@claims"})
	_ = c.Add("labul", endpoint.Endpoint{URL: labulURL})
	_ = c.Use("labda")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAPITargetPrecedence(t *testing.T) {
	path := isolateEndpoints(t)
	writeEndpoints(t, path)

	check := func(name, flag, wantURL, wantName string) {
		t.Helper()
		got, err := resolveAPITarget(flag, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.URL != wantURL || got.Name != wantName {
			t.Errorf("%s: got %s (name %q, from %s), want %s (%q)", name, got.URL, got.Name, got.Source, wantURL, wantName)
		}
	}

	check("current endpoint", "", labdaURL, "labda")
	t.Setenv(endpoint.EnvEndpoint, "labul")
	check("$CLAIM_ENDPOINT over current", "", labulURL, "labul")
	endpointFlag = "labda"
	check("--endpoint over $CLAIM_ENDPOINT", "", labdaURL, "labda")
	t.Setenv("CLAIM_API_URL", labulURL+"/")
	check("$CLAIM_API_URL over --endpoint (named by URL)", "", labulURL+"/", "labul")
	check("-a over everything", "https://other", "https://other", "")

	t.Setenv("CLAIM_API_URL", "")
	endpointFlag = "nope"
	if _, err := resolveAPITarget("", false); err == nil || !strings.Contains(err.Error(), "configured: labda, labul") {
		t.Errorf("unknown endpoint: %v", err)
	}
}

// Real failure of claims#114: CLAIM_API_URL unset silently meant
// http://localhost:8080, which answered 404. Now nothing set is an error
// that names the fix -- or, on a terminal, the picker.
func TestResolveAPITargetNoLocalhost(t *testing.T) {
	isolateEndpoints(t)
	_, err := resolveAPITarget("", false)
	if !errors.Is(err, errNoEndpoint) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"no localhost default", "CLAIM_API_URL", "--endpoint", "claims endpoint add"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}

	pickEndpoint = func(c *endpoint.Config) (string, error) { return "https://typed", nil }
	got, err := resolveAPITarget("", true)
	if err != nil || got.URL != "https://typed" {
		t.Errorf("interactive: %v %v", got, err)
	}
}

func TestEndpointCommands(t *testing.T) {
	path := isolateEndpoints(t)
	var out bytes.Buffer
	if err := runEndpointList(&out); err != nil || !strings.Contains(out.String(), "No endpoints") {
		t.Errorf("empty list: %q %v", out.String(), err)
	}

	endpointAddFlags = endpoint.Endpoint{Issuer: "https://idp", ClientID: "1@claims"}
	endpointAddUse = true
	t.Cleanup(func() { endpointAddFlags, endpointAddUse = endpoint.Endpoint{}, false })
	out.Reset()
	if err := runEndpointAdd(&out, "labda", labdaURL); err != nil || !strings.Contains(out.String(), "current endpoint") {
		t.Fatalf("add: %q %v", out.String(), err)
	}
	endpointAddFlags, endpointAddUse = endpoint.Endpoint{}, false
	out.Reset()
	if err := runEndpointAdd(&out, "labul", labulURL); err != nil || !strings.Contains(out.String(), "--issuer") {
		t.Fatalf("add w/o login: %q %v", out.String(), err)
	}

	out.Reset()
	_ = runEndpointList(&out)
	if !strings.Contains(out.String(), "* labda") || !strings.Contains(out.String(), "  labul") {
		t.Errorf("list:\n%s", out.String())
	}

	if err := updateEndpoints(&out, func(c *endpoint.Config) (string, error) { return "", c.Use("labul") }); err != nil {
		t.Fatal(err)
	}
	c, _ := endpoint.Load(path)
	if c.Current != "labul" {
		t.Errorf("current = %q", c.Current)
	}
}

// `claims login --endpoint labda` needs no --issuer/--client-id when the
// endpoint has them; flags still win.
func TestLoginConfigFromEndpoint(t *testing.T) {
	path := isolateEndpoints(t)
	writeEndpoints(t, path)
	endpointFlag = "labda"
	target, err := singleAPIURL("")
	if err != nil {
		t.Fatal(err)
	}
	cfg := loginConfigFor(target)
	if cfg.Issuer != "https://zitadel.example" || cfg.ClientID != "42@claims" {
		t.Errorf("cfg = %+v", cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
	loginFlags.ClientID = "flag@claims"
	if cfg := loginConfigFor(target); cfg.ClientID != "flag@claims" || cfg.Issuer != "https://zitadel.example" {
		t.Errorf("flag override: %+v", cfg)
	}
}

func endpointWithLogin() endpoint.Endpoint {
	return endpoint.Endpoint{URL: labdaURL, Issuer: "https://zitadel.example", ClientID: "42@claims"}
}
