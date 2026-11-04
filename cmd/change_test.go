package cmd

import (
	"encoding/json"
	"fmt"
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
