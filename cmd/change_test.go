package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGH answers the gh calls claims change makes and records them.
type fakeGH struct {
	calls      [][]string
	conclusion string
	log        string
}

func (f *fakeGH) install(t *testing.T) {
	t.Helper()
	orig, origPoll := gh, changePoll
	t.Cleanup(func() { gh, changePoll = orig, origPoll })
	changePoll = time.Millisecond
	gh = func(args ...string) ([]byte, error) {
		f.calls = append(f.calls, args)
		switch strings.Join(args[:2], " ") {
		case "workflow run":
			return nil, nil
		case "run list":
			b, _ := json.Marshal([]map[string]any{
				{"databaseId": 1, "createdAt": time.Now().Add(-time.Hour), "status": "completed", "conclusion": "success"},
				{"databaseId": 42, "createdAt": time.Now(), "status": "in_progress"},
			})
			return b, nil
		case "run view":
			if len(args) > 3 && args[3] == "--log-failed" {
				return []byte(f.log), nil
			}
			return []byte(fmt.Sprintf(`{"status":"completed","conclusion":%q}`, f.conclusion)), nil
		case "pr list":
			return []byte(`[{"url":"https://github.com/o/r/pull/7"}]`), nil
		}
		return nil, fmt.Errorf("unexpected gh %v", args)
	}
}

func (f *fakeGH) call(prefix string) []string {
	for _, c := range f.calls {
		if strings.HasPrefix(strings.Join(c, " "), prefix) {
			return c
		}
	}
	return nil
}

func TestChangeDispatchesWorkflow(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	t.Chdir(root)
	f := &fakeGH{conclusion: "success"}
	f.install(t)

	cfg := &ChangeConfig{Name: "dev7-vm", Size: "xlarge", Reboot: "2026-11-05T20:00", RequestedBy: "jdoe", Reason: "more RAM", Repo: "o/r", Ref: "main"}
	if err := runChange(cfg, false); err != nil {
		t.Fatal(err)
	}
	run := strings.Join(f.call("workflow run"), " ")
	for _, want := range []string{
		"workflow run dispatch-change-claim.yaml --ref main -R o/r",
		"-f kind=NativeVsphereVM", "-f name=dev7-vm", "-f namespace=default",
		"-f source_path=" + fxDir + "/dev7.yaml", "-f size=xlarge",
		"-f reboot=at", "-f reboot_at=2026-11-05T20:00", "-f requested_by=jdoe", "-f reason=more RAM",
	} {
		if !strings.Contains(run, want) {
			t.Errorf("gh call lacks %q:\n%s", want, run)
		}
	}
	if v := strings.Join(f.call("run view 42"), " "); v == "" {
		t.Errorf("did not wait for the new run (42): %v", f.calls)
	}
	if p := strings.Join(f.call("pr list"), " "); !strings.Contains(p, "--head change-claim/dev7-vm") {
		t.Errorf("pr lookup = %s", p)
	}
}

func TestChangeReportsRefusal(t *testing.T) {
	f := &fakeGH{conclusion: "failure", log: "Create-Change-PR\tRewrite\t2026 ::error::size 'huge' is not one of ['large', 'medium', 'small', 'xlarge']\n"}
	f.install(t)
	cfg := &ChangeConfig{Kind: "NativeVsphereVM", Name: "dev7-vm", SourcePath: fxDir + "/dev7.yaml", Size: "huge", Ref: "main"}
	err := runChange(cfg, false)
	if err == nil || !strings.Contains(err.Error(), "failure") || !strings.Contains(err.Error(), "size 'huge' is not one of") {
		t.Errorf("err = %v", err)
	}
}

func TestChangeDryRunStartsNothing(t *testing.T) {
	f := &fakeGH{}
	f.install(t)
	cfg := &ChangeConfig{Kind: "NativeProxmoxVM", Name: "x-vm", SourcePath: fxDir + "/x.yaml", Size: "large", Reboot: "none", DryRun: true, Ref: "main"}
	if err := runChange(cfg, false); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Errorf("dry run called gh: %v", f.calls)
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
	f := &fakeGH{conclusion: "success"}
	f.install(t)

	for _, tc := range []struct {
		cfg  ChangeConfig
		want string
	}{
		{ChangeConfig{Name: "a1-vm", Retry: true, Reboot: "evening", Reason: "collection fixed", Ref: "main"}, "-f retry=true"},
		{ChangeConfig{Name: "a1-vm", RunID: "fix-1", Reboot: "evening", Ref: "main"}, "-f run_id=fix-1"},
	} {
		f.calls = nil
		cfg := tc.cfg
		if err := runChange(&cfg, false); err != nil {
			t.Fatal(err)
		}
		run := strings.Join(f.call("workflow run"), " ")
		if !strings.Contains(run, tc.want) || strings.Contains(run, "size=") || strings.Contains(run, "reboot") {
			t.Errorf("gh call = %s, want %q and no size/reboot", run, tc.want)
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
