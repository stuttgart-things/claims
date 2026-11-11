package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stuttgart-things/claims/internal/apiauth"
	"github.com/stuttgart-things/claims/internal/endpoint"
	"github.com/stuttgart-things/claims/internal/ghclient"
)

// TestMain keeps GitHub out of the tests: without this, a test outside a
// checkout would read the fleet repository with the developer's gh login.
func TestMain(m *testing.M) {
	openRemoteFleet = func(repo, ref string) (*fleetSource, error) {
		return nil, errors.New("remote fleet disabled in tests")
	}
	ghclient.GHAuthToken = func() (string, error) { return "", errors.New("gh disabled in tests") }
	// Requests that a test does not point at its own server go nowhere.
	_ = os.Setenv(ghclient.EnvAPIURL, "http://127.0.0.1:1/")
	// Never the developer's endpoints or logins (claims#116).
	if dir, err := os.MkdirTemp("", "claims-test-config-*"); err == nil {
		_ = os.Setenv(endpoint.EnvConfigFile, filepath.Join(dir, "config.yaml"))
		_ = os.Setenv(apiauth.EnvCredentialsFile, filepath.Join(dir, "credentials.json"))
	}
	os.Exit(m.Run())
}

// stubRemoteFleet serves files as the GitHub repository and records the
// repository and ref asked for.
func stubRemoteFleet(t *testing.T, files fstest.MapFS) (asked *[2]string) {
	t.Helper()
	asked = new([2]string)
	prev := openRemoteFleet
	openRemoteFleet = func(repo, ref string) (*fleetSource, error) {
		asked[0], asked[1] = repo, ref
		return &fleetSource{FS: files, Repo: repo, Ref: ref}, nil
	}
	t.Cleanup(func() { openRemoteFleet = prev })
	return asked
}

func ansibleVMDoc(name, file, runID string) string {
	rid := ""
	if runID != "" {
		rid = "\n    runID: " + runID
	}
	return "apiVersion: resources.stuttgart-things.com/v1alpha1\nkind: NativeProxmoxVM\nmetadata:\n  name: " + name +
		"\n  annotations:\n    stuttgart-things.com/source-path: " + file +
		"\nspec:\n  ansible:\n    enabled: true" + rid + "\n    pipelineNamespace: tekton-ci\n"
}

func TestFleetRepoName(t *testing.T) {
	t.Setenv(envFleetRepo, "")
	if got := fleetRepoName(""); got != defaultFleetRepo {
		t.Errorf("default = %q", got)
	}
	t.Setenv(envFleetRepo, "acme/env")
	if got := fleetRepoName(""); got != "acme/env" {
		t.Errorf("env = %q", got)
	}
	if got := fleetRepoName("acme/flag"); got != "acme/flag" {
		t.Errorf("flag = %q", got)
	}
}

func TestLocalFleet(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	if src, ok := localFleet(filepath.Join(root, fxDir)); !ok || src.Root != root || src.remote() {
		t.Errorf("fixture checkout: %+v, %v", src, ok)
	}

	// A git checkout without *-xrs directories (claims itself, say) is not
	// the fleet repository.
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := localFleet(other); ok {
		t.Error("a checkout without *-xrs directories counts as the fleet")
	}
}

func TestOpenFleetPrefersCheckout(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	t.Chdir(root)
	asked := stubRemoteFleet(t, fstest.MapFS{})
	src, err := openFleet("acme/fleet", "main")
	if err != nil || src.remote() || asked[0] != "" {
		t.Errorf("src = %+v, err = %v, remote asked %v", src, err, asked)
	}
}

func TestChangeRemoteFleet(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(envFleetRepo, "")
	file := fxDir + "/sbx.yaml"
	asked := stubRemoteFleet(t, fstest.MapFS{
		file:                          {Data: []byte(ansibleVMDoc("sbx-vm", file, `"2"`))},
		fxDir + "/kustomization.yaml": {Data: []byte("resources: []\n")},
	})

	c := ChangeConfig{Name: "sbx-vm", Retry: true, Ref: "main"}
	if err := resolveChange(&c); err != nil {
		t.Fatal(err)
	}
	if asked[0] != defaultFleetRepo || asked[1] != "main" {
		t.Errorf("read %s@%s, want %s@main", asked[0], asked[1], defaultFleetRepo)
	}
	if c.Kind != "NativeProxmoxVM" || c.SourcePath != file {
		t.Errorf("resolved %s %s", c.Kind, c.SourcePath)
	}
	if c.Repo != defaultFleetRepo {
		t.Errorf("Repo = %q: the workflow must start in the repository the claim was read from", c.Repo)
	}
	if got := runIDLine(&c); got != "2 -> 3, PipelineRun sbx-vm-ansible-3" {
		t.Errorf("runIDLine = %q", got)
	}

	// The id is checked against the remote file, and the error says where.
	c = ChangeConfig{Name: "sbx-vm", RunID: "2", Repo: "acme/fleet", Ref: "dev"}
	err := resolveChange(&c)
	if err == nil || !strings.Contains(err.Error(), "already") || !strings.Contains(err.Error(), "github.com/acme/fleet@dev") {
		t.Errorf("err = %v", err)
	}

	// All three given: no name lookup, but the runID preview still reads GitHub.
	c = ChangeConfig{Kind: "NativeProxmoxVM", Name: "sbx-vm", SourcePath: file, Retry: true, Ref: "main"}
	if err := resolveChange(&c); err != nil || !c.runIDSeen || c.runIDNext != "3" {
		t.Errorf("explicit flags: %v, %+v", err, c)
	}
}

func TestChangeRemoteFleetUnavailable(t *testing.T) {
	t.Chdir(t.TempDir())
	// TestMain's stub: no remote fleet.
	c := ChangeConfig{Name: "sbx-vm", Retry: true, Ref: "main"}
	err := resolveChange(&c)
	if err == nil || !strings.Contains(err.Error(), "--source-path") {
		t.Errorf("err = %v, want the hint to pass --kind/--name/--source-path", err)
	}
}
