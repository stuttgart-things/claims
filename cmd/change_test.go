package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"

	"github.com/stuttgart-things/claims/internal/ghclient"
)

func TestChangeDispatchesWorkflow(t *testing.T) {
	for _, runDetails := range []bool{true, false} {
		t.Run(fmt.Sprintf("runDetails=%v", runDetails), func(t *testing.T) {
			root, _ := xrFixtureRepo(t)
			t.Chdir(root)
			f := &fakeGitHubAPI{runDetails: runDetails, conclusion: "success"}
			f.install(t)

			cfg := &ChangeConfig{Name: "dev7-vm", Size: "xlarge", Reboot: "2026-11-05T20:00", RequestedBy: "jdoe", Reason: "more RAM", Repo: "o/r", Ref: "main"}
			if err := runChange(cfg, false); err != nil {
				t.Fatal(err)
			}
			if c := f.called("POST /repos/o/r/actions/workflows/" + changeWorkflow + "/dispatches"); c == "" || f.dispatch != "main" {
				t.Errorf("no dispatch in o/r on main: %v", f.calls)
			}
			want := map[string]any{
				"kind": "NativeVsphereVM", "name": "dev7-vm", "namespace": "default",
				"source_path": fxDir + "/dev7.yaml", "size": "xlarge",
				"reboot": "at", "reboot_at": "2026-11-05T20:00", "requested_by": "jdoe", "reason": "more RAM",
			}
			if !reflect.DeepEqual(f.inputs, want) {
				t.Errorf("inputs = %v\nwant %v", f.inputs, want)
			}
			if f.called("GET /repos/o/r/actions/runs/42") == "" {
				t.Errorf("did not wait for the new run (42): %v", f.calls)
			}
			if listed := f.called("GET /repos/o/r/actions/workflows/") != ""; listed == runDetails {
				t.Errorf("listed the runs = %v with runDetails = %v", listed, runDetails)
			}
			if p := f.called("GET /repos/o/r/pulls"); !strings.Contains(p, "head=o%3Achange-claim%2Fdev7-vm") || !strings.Contains(p, "state=open") {
				t.Errorf("pr lookup = %q", p)
			}
		})
	}
}

func TestChangeRequestedByFromAPI(t *testing.T) {
	f := &fakeGitHubAPI{runDetails: true, conclusion: "success", login: "api-user"}
	f.install(t)
	cfg := &ChangeConfig{Kind: "NativeProxmoxVM", Name: "x-vm", SourcePath: fxDir + "/x.yaml", Size: "large", Reboot: "none", Repo: "o/r", Ref: "main"}
	if err := runChange(cfg, false); err != nil {
		t.Fatal(err)
	}
	if f.inputs["requested_by"] != "api-user" {
		t.Errorf("requested_by = %v", f.inputs["requested_by"])
	}
}

func TestChangeReportsRefusal(t *testing.T) {
	f := &fakeGitHubAPI{runDetails: true, conclusion: "failure", annotations: []string{
		"size 'huge' is not one of ['large', 'medium', 'small', 'xlarge']", "Process completed with exit code 1.",
	}}
	f.install(t)
	cfg := &ChangeConfig{Kind: "NativeVsphereVM", Name: "dev7-vm", SourcePath: fxDir + "/dev7.yaml", Size: "huge", Ref: "main"}
	err := runChange(cfg, false)
	if err == nil || !strings.Contains(err.Error(), "failure") || !strings.Contains(err.Error(), "size 'huge' is not one of") ||
		strings.Contains(err.Error(), "Process completed") || strings.Contains(err.Error(), "ubuntu-latest") {
		t.Errorf("err = %v", err)
	}
	if cfg.Repo != defaultFleetRepo {
		t.Errorf("Repo = %q: outside a fleet checkout the workflow runs in %s", cfg.Repo, defaultFleetRepo)
	}
}

func TestChangeAPIErrors(t *testing.T) {
	f := &fakeGitHubAPI{}
	f.install(t)
	t.Setenv("GITHUB_TOKEN", "wrong")
	cfg := &ChangeConfig{Kind: "NativeVsphereVM", Name: "dev7-vm", SourcePath: fxDir + "/dev7.yaml", Size: "large", Repo: "o/r", Ref: "main", RequestedBy: "jdoe"}
	err := runChange(cfg, false)
	if err == nil || !strings.Contains(err.Error(), "token was refused") || !strings.Contains(err.Error(), "o/r") {
		t.Errorf("err = %v", err)
	}

	t.Setenv("GITHUB_TOKEN", "")
	err = runChange(&ChangeConfig{Kind: "NativeVsphereVM", Name: "dev7-vm", SourcePath: fxDir + "/dev7.yaml", Size: "large", Repo: "o/r", Ref: "main"}, false)
	if !errors.Is(err, ghclient.ErrNoToken) {
		t.Errorf("err = %v, want ErrNoToken", err)
	}
}

func TestRefusalLines(t *testing.T) {
	if got := refusalLines([]string{"Process completed with exit code 1."}, "https://x/run"); !strings.Contains(got, "https://x/run") {
		t.Errorf("no refusal: %q", got)
	}
	if got := refusalLines([]string{" a ", "b"}, ""); got != "  a\n  b" {
		t.Errorf("got %q", got)
	}
}

func TestChangeRepoFromCheckout(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	t.Chdir(root)
	// The fixture's origin is a local path: no GitHub repository to start the workflow in.
	cfg := &ChangeConfig{Name: "dev7-vm", Size: "large", Ref: "main", DryRun: true}
	if err := runChange(cfg, false); err == nil || !strings.Contains(err.Error(), "--repo") {
		t.Errorf("err = %v, want a hint to pass --repo", err)
	}
	g, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteRemote("origin"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{"git@github.com:acme/fleet.git"}}); err != nil {
		t.Fatal(err)
	}
	cfg = &ChangeConfig{Name: "dev7-vm", Size: "large", Ref: "main", DryRun: true}
	if err := runChange(cfg, false); err != nil || cfg.Repo != "acme/fleet" {
		t.Errorf("Repo = %q, %v", cfg.Repo, err)
	}
}

func TestChangeDryRunStartsNothing(t *testing.T) {
	f := &fakeGitHubAPI{}
	f.install(t)
	cfg := &ChangeConfig{Kind: "NativeProxmoxVM", Name: "x-vm", SourcePath: fxDir + "/x.yaml", Size: "large", Reboot: "none", DryRun: true, Ref: "main"}
	if err := runChange(cfg, false); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Errorf("dry run called GitHub: %v", f.calls)
	}
}

func TestResolveChange(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	t.Chdir(root)
	cases := []struct {
		name string
		cfg  ChangeConfig
		want string // error substring, "" = ok
		then func(t *testing.T, c ChangeConfig)
	}{
		{name: "by name, default reboot", cfg: ChangeConfig{Name: "dev8-vm", Size: "large"}, then: func(t *testing.T, c ChangeConfig) {
			if c.Kind != "NativeVsphereVM" || c.SourcePath != fxDir+"/dev8.yaml" || c.Reboot != "evening" {
				t.Errorf("resolved %+v", c)
			}
		}},
		{name: "by source-path", cfg: ChangeConfig{SourcePath: fxDir + "/dev7.yaml", Size: "large", Reboot: "now"}, then: func(t *testing.T, c ChangeConfig) {
			if c.Name != "dev7-vm" || c.Reboot != "now" {
				t.Errorf("resolved %+v", c)
			}
		}},
		{name: "size required", cfg: ChangeConfig{Name: "dev7-vm"}, want: "--size"},
		{name: "unknown claim", cfg: ChangeConfig{Name: "nope-vm", Size: "large"}, want: "no deletable claim"},
		{name: "not a VM", cfg: ChangeConfig{Kind: "MinioBucket", Name: "b", SourcePath: fxDir + "/b.yaml", Size: "large"}, want: "only NativeVsphereVM and NativeProxmoxVM"},
		{name: "bad reboot", cfg: ChangeConfig{Name: "dev7-vm", Size: "large", Reboot: "tomorrow"}, want: "--reboot"},
		{name: "at without time", cfg: ChangeConfig{Name: "dev7-vm", Size: "large", Reboot: "at"}, want: "needs a time"},
		{name: "path outside *-xrs", cfg: ChangeConfig{Kind: "NativeVsphereVM", Name: "v", SourcePath: "clusters/x/y/z/v.yaml", Size: "large"}, want: "*-xrs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.cfg
			err := resolveChange(&c)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if tc.then != nil {
					tc.then(t, c)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// fxAnsibleVM is a VM order whose AnsibleRun is enabled, with an optional runID.
func fxAnsibleVM(t *testing.T, root, name, runID string) string {
	t.Helper()
	file := fxDir + "/" + strings.TrimSuffix(name, "-vm") + ".yaml"
	rid := ""
	if runID != "" {
		rid = "\n    runID: " + runID
	}
	doc := "apiVersion: resources.stuttgart-things.com/v1alpha1\nkind: NativeVsphereVM\nmetadata:\n  name: " + name +
		"\n  annotations:\n    stuttgart-things.com/source-path: " + file +
		"\nspec:\n  vm:\n    cpu: \"4\"\n  ansible:\n    enabled: true" + rid + "\n    pipelineNamespace: tekton-ci\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(file)), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestResolveChangeRunID(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	t.Chdir(root)
	fxAnsibleVM(t, root, "a1-vm", "")
	fxAnsibleVM(t, root, "a2-vm", `"2"`)
	fxAnsibleVM(t, root, "a3-vm", `"fix-docker"`)

	cases := []struct {
		name string
		cfg  ChangeConfig
		want string // error substring, "" = ok
		then func(t *testing.T, c ChangeConfig)
	}{
		{name: "retry from unset", cfg: ChangeConfig{Name: "a1-vm", Retry: true, Reboot: "evening"}, then: func(t *testing.T, c ChangeConfig) {
			if c.runIDNext != "2" || c.runIDCurrent != "" || c.Reboot != "" || c.pipelineNS != "tekton-ci" {
				t.Errorf("resolved %+v", c)
			}
			if got := runIDLine(&c); got != "(unset) -> 2, PipelineRun a1-vm-ansible-2" {
				t.Errorf("runIDLine = %q", got)
			}
		}},
		{name: "retry counts up", cfg: ChangeConfig{Name: "a2-vm", Retry: true}, then: func(t *testing.T, c ChangeConfig) {
			if c.runIDNext != "3" || c.runIDCurrent != "2" {
				t.Errorf("resolved %+v", c)
			}
			if w := runIDWarning(&c); !strings.Contains(w, "a2-vm-ansible-2 (namespace tekton-ci)") {
				t.Errorf("warning = %q", w)
			}
		}},
		{name: "retry after a named id", cfg: ChangeConfig{Name: "a3-vm", Retry: true}, want: "pass the next one with --run-id"},
		{name: "explicit id", cfg: ChangeConfig{Name: "a3-vm", RunID: "fix-docker-2"}},
		{name: "same id", cfg: ChangeConfig{Name: "a2-vm", RunID: "2"}, want: "already"},
		{name: "invalid id", cfg: ChangeConfig{Name: "a1-vm", RunID: "Fix_1"}, want: "--run-id"},
		{name: "ansible not enabled", cfg: ChangeConfig{Name: "dev7-vm", Retry: true}, want: "spec.ansible.enabled is not true"},
		{name: "run-id and retry", cfg: ChangeConfig{Name: "a1-vm", RunID: "x", Retry: true}, want: "exclude each other"},
		{name: "size and run-id", cfg: ChangeConfig{Name: "a1-vm", RunID: "x", Size: "large"}, want: "two changes"},
		{name: "reboot with run-id", cfg: ChangeConfig{Name: "a1-vm", Retry: true, Reboot: "now", rebootSet: true}, want: "--reboot belongs to a resize"},
		{name: "nothing to change", cfg: ChangeConfig{Name: "a1-vm"}, want: "--size, --run-id or --retry"},
		{name: "not a VM", cfg: ChangeConfig{Kind: "MinioBucket", Name: "b", SourcePath: fxDir + "/b.yaml", Retry: true}, want: "resized or re-run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.cfg
			err := resolveChange(&c)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if tc.then != nil {
					tc.then(t, c)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestChangeRunIDOutsideCheckout(t *testing.T) {
	t.Chdir(t.TempDir())
	c := ChangeConfig{Kind: "NativeProxmoxVM", Name: "x-vm", SourcePath: fxDir + "/x.yaml", Retry: true}
	if err := resolveChange(&c); err != nil {
		t.Fatal(err)
	}
	if c.runIDSeen || !strings.Contains(runIDLine(&c), "the workflow counts up") {
		t.Errorf("resolved %+v, line %q", c, runIDLine(&c))
	}
}

func TestChangeRunIDInputs(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	t.Chdir(root)
	fxAnsibleVM(t, root, "a1-vm", "")
	f := &fakeGitHubAPI{runDetails: true, conclusion: "success"}
	f.install(t)

	for _, tc := range []struct {
		cfg       ChangeConfig
		key, want string
	}{
		{ChangeConfig{Name: "a1-vm", Retry: true, Reboot: "evening", Reason: "collection fixed", Repo: "o/r", Ref: "main", RequestedBy: "jdoe"}, "retry", "true"},
		{ChangeConfig{Name: "a1-vm", RunID: "fix-1", Reboot: "evening", Repo: "o/r", Ref: "main", RequestedBy: "jdoe"}, "run_id", "fix-1"},
	} {
		cfg := tc.cfg
		if err := runChange(&cfg, false); err != nil {
			t.Fatal(err)
		}
		_, size := f.inputs["size"]
		_, reboot := f.inputs["reboot"]
		if f.inputs[tc.key] != tc.want || size || reboot {
			t.Errorf("inputs = %v, want %s=%s and no size/reboot", f.inputs, tc.key, tc.want)
		}
	}
}

func TestNextRunID(t *testing.T) {
	for in, want := range map[string]string{"": "2", "2": "3", "9": "10"} {
		if got, err := nextRunID(in); err != nil || got != want {
			t.Errorf("nextRunID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"fix-1", "02", "-1"} {
		if _, err := nextRunID(in); err == nil {
			t.Errorf("nextRunID(%q) accepted", in)
		}
	}
}

func TestClaimArg(t *testing.T) {
	c := ChangeConfig{}
	if err := claimArg(&c, "dev7-vm"); err != nil || c.Name != "dev7-vm" {
		t.Errorf("name: %+v %v", c, err)
	}
	c = ChangeConfig{}
	if err := claimArg(&c, fxDir+"/dev7.yaml"); err != nil || c.SourcePath != fxDir+"/dev7.yaml" {
		t.Errorf("path: %+v %v", c, err)
	}
	c = ChangeConfig{Name: "other-vm"}
	if err := claimArg(&c, "dev7-vm"); err == nil {
		t.Error("conflicting name accepted")
	}
}
