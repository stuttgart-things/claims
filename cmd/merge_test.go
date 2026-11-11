package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/stuttgart-things/claims/internal/ghclient"
)

// fakeMergeAPI answers the GitHub API calls of claims merge for the
// repository o/r and records them as "METHOD /path?query".
type fakeMergeAPI struct {
	prs        map[int]map[string]any // number -> PR JSON
	checks     [][]map[string]any     // check runs per read (the last one repeats)
	statuses   []map[string]any       // commit statuses
	states     []string               // mergeable_state per PR read (the last one repeats)
	protection map[string]any         // GET /branches/main "protection"
	rules      []map[string]any       // GET /rules/branches/main (nil: 403 as on a free plan)
	reviews    []map[string]any
	mergeCode  int // status of PUT .../merge (0 = 200)
	mergeMsg   string

	mu        sync.Mutex
	calls     []string
	checkRead int
	stateRead int
	merged    map[string]any // body of the merge
}

func (f *fakeMergeAPI) install(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv(ghclient.EnvAPIURL, srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GIT_TOKEN", "")
	for _, v := range []*time.Duration{&mergePoll, &mergeCheckGrace} {
		orig := *v
		*v = time.Millisecond
		t.Cleanup(func() { *v = orig })
	}
	origTTY, origConfirm := stdinIsTerminal, confirmMerge
	stdinIsTerminal = func() bool { return false }
	confirmMerge = func(string, string) (bool, error) {
		t.Error("asked without a terminal")
		return false, nil
	}
	t.Cleanup(func() { stdinIsTerminal, confirmMerge = origTTY, origConfirm })
}

func (f *fakeMergeAPI) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeMergeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	f.calls = append(f.calls, call)
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	js := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	path, ok := strings.CutPrefix(r.URL.Path, "/repos/o/r")
	if !ok {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case path == "/pulls" && r.Method == http.MethodGet:
		head := strings.TrimPrefix(r.URL.Query().Get("head"), "o:")
		var out []map[string]any
		for _, pr := range f.prs {
			if pr["head"].(map[string]any)["ref"] == head && pr["state"] == "open" {
				out = append(out, pr)
			}
		}
		js(out)
	case len(parts) == 2 && parts[0] == "pulls":
		n, _ := strconv.Atoi(parts[1])
		pr, ok := f.prs[n]
		if !ok {
			http.NotFound(w, r)
			return
		}
		cp := map[string]any{}
		for k, v := range pr {
			cp[k] = v
		}
		if len(f.states) > 0 {
			cp["mergeable_state"] = f.states[min(f.stateRead, len(f.states)-1)]
			f.stateRead++
		}
		js(cp)
	case len(parts) == 3 && parts[0] == "pulls" && parts[2] == "files":
		js([]map[string]any{{"filename": "clusters/labda/vsphere/machinery-orders-xrs/dev7.yaml", "status": "added"}})
	case len(parts) == 3 && parts[0] == "pulls" && parts[2] == "reviews":
		js(f.reviews)
	case len(parts) == 3 && parts[0] == "pulls" && parts[2] == "merge" && r.Method == http.MethodPut:
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if f.mergeCode != 0 {
			w.WriteHeader(f.mergeCode)
			js(map[string]any{"message": f.mergeMsg})
			return
		}
		f.merged = body
		js(map[string]any{"sha": "abcdef0123456789", "merged": true})
	case strings.HasPrefix(path, "/git/refs/heads/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	case path == "/branches/main":
		b := map[string]any{"name": "main", "protected": f.protection != nil}
		if f.protection != nil {
			b["protection"] = f.protection
		}
		js(b)
	case path == "/rules/branches/main":
		if f.rules == nil {
			w.WriteHeader(http.StatusForbidden)
			js(map[string]any{"message": "Upgrade to GitHub Pro"})
			return
		}
		js(f.rules)
	case len(parts) == 3 && parts[0] == "commits" && parts[2] == "check-runs":
		var runs []map[string]any
		if len(f.checks) > 0 {
			runs = f.checks[min(f.checkRead, len(f.checks)-1)]
			f.checkRead++
		}
		js(map[string]any{"total_count": len(runs), "check_runs": runs})
	case len(parts) == 3 && parts[0] == "commits" && parts[2] == "status":
		js(map[string]any{"state": "success", "statuses": f.statuses})
	default:
		http.NotFound(w, r)
	}
}

func fakePR(n int, branch, title string) map[string]any {
	return map[string]any{
		"number": n, "state": "open", "title": title, "mergeable_state": "clean",
		"html_url": "https://github.com/o/r/pull/" + strconv.Itoa(n),
		"user":     map[string]any{"login": "jdoe"},
		"labels":   []map[string]any{{"name": "delete-claim"}},
		"head":     map[string]any{"ref": branch, "sha": "1234567890abcdef", "repo": map[string]any{"full_name": "o/r"}},
		"base":     map[string]any{"ref": "main", "repo": map[string]any{"full_name": "o/r", "default_branch": "main"}},
	}
}

func checkRun(name, status, conclusion string) map[string]any {
	return map[string]any{"name": name, "status": status, "conclusion": conclusion}
}

var greenChecks = []map[string]any{checkRun("validate-artifacts", "completed", "success"), checkRun("Delete-XR", "completed", "skipped")}

func TestParsePRRef(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want prRef
		err  bool
	}{
		{arg: "4442", want: prRef{number: 4442}},
		{arg: "#7", want: prRef{number: 7}},
		{arg: "https://github.com/o/r/pull/12", want: prRef{repo: "o/r", number: 12}},
		{arg: "https://github.com/o/r/pull/12/files", want: prRef{repo: "o/r", number: 12}},
		{arg: "vm-dev7", want: prRef{name: "vm-dev7"}},
		{arg: "https://github.com/o/r/issues/3", err: true},
		{arg: "0", err: true},
		{arg: "", err: true},
	} {
		got, err := parsePRRef(tc.arg)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("parsePRRef(%q) = %+v, %v", tc.arg, got, err)
		}
	}
}

func TestMergeBranchCandidates(t *testing.T) {
	for arg, want := range map[string][]string{
		"delete-claim/dev7-vm": {"delete-claim/dev7-vm"},
		"vm-dev7":              {"vm-dev7"},
		"dev7": {"vm-dev7", "change-claim/dev7", "delete-claim/dev7",
			"change-claim/dev7-vm", "delete-claim/dev7-vm"},
		"dev7-vm": {"vm-dev7-vm", "vm-dev7", "change-claim/dev7-vm", "delete-claim/dev7-vm"},
	} {
		if got := mergeBranchCandidates(arg); !reflect.DeepEqual(got, want) {
			t.Errorf("mergeBranchCandidates(%q) = %v, want %v", arg, got, want)
		}
	}
}

func TestMergeConsequence(t *testing.T) {
	for _, tc := range []struct{ branch, title, want string }{
		{"vm-dev7", "feat(machinery): VM dev7 (dev, labda-vsphere) (MERGE = BAU)", "MERGE = BAU: merging builds dev7"},
		{"delete-claim/dev7-vm", "Delete claim: NativeVsphereVM/dev7-vm", "DELETE: merging deletes NativeVsphereVM/dev7-vm"},
		{"change-claim/dev7-vm", "x", "CHANGE: merging applies the change to dev7-vm"},
	} {
		pr := decodePR(t, fakePR(1, tc.branch, tc.title))
		if got := mergeConsequence(pr); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: %q, want prefix %q", tc.branch, got, tc.want)
		}
	}
}

func TestMergeByNameSquashesAndDeletesBranch(t *testing.T) {
	f := &fakeMergeAPI{
		prs:    map[int]map[string]any{9: fakePR(9, "delete-claim/dev7-vm", "Delete claim: NativeVsphereVM/dev7-vm")},
		checks: [][]map[string]any{greenChecks},
	}
	f.install(t)
	var out bytes.Buffer
	if err := runMerge(context.Background(), &MergeConfig{Arg: "dev7", Repo: "o/r", Yes: true}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if f.merged["merge_method"] != "squash" || f.merged["sha"] != "1234567890abcdef" ||
		f.merged["commit_title"] != "Delete claim: NativeVsphereVM/dev7-vm (#9)" {
		t.Errorf("merge body = %v", f.merged)
	}
	if !f.called("DELETE /repos/o/r/git/refs/heads/delete-claim/dev7-vm") {
		t.Errorf("branch not deleted: %v", f.calls)
	}
	for _, want := range []string{"DELETE: merging deletes NativeVsphereVM/dev7-vm", "machinery-orders-xrs/dev7.yaml", "delete-claim", "no required checks", "Merged PR #9"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestMergeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		f       *fakeMergeAPI
		cfg     MergeConfig
		wantErr string
	}{
		{
			name:    "failed check",
			f:       &fakeMergeAPI{checks: [][]map[string]any{{checkRun("validate-artifacts", "completed", "failure")}}},
			cfg:     MergeConfig{Wait: true},
			wantErr: "1 check failed; the PR stays open",
		},
		{
			name:    "failed status",
			f:       &fakeMergeAPI{checks: [][]map[string]any{greenChecks}, statuses: []map[string]any{{"context": "ci/x", "state": "error"}}},
			wantErr: "ci/x: failure",
		},
		{
			name:    "running check without --wait",
			f:       &fakeMergeAPI{checks: [][]map[string]any{{checkRun("Verify-Claim-Deletion", "in_progress", "")}}},
			wantErr: "still running: wait for them (--wait)",
		},
		{
			name: "fail fast while waiting",
			f: &fakeMergeAPI{checks: [][]map[string]any{
				{checkRun("a", "in_progress", ""), checkRun("b", "queued", "")},
				{checkRun("a", "completed", "timed_out"), checkRun("b", "queued", "")},
			}},
			cfg:     MergeConfig{Wait: true},
			wantErr: "a: failure",
		},
		{
			name:    "timeout",
			f:       &fakeMergeAPI{checks: [][]map[string]any{{checkRun("a", "in_progress", "")}}},
			cfg:     MergeConfig{Wait: true, Timeout: 20 * time.Millisecond},
			wantErr: "did not finish within",
		},
		{
			name: "blocked: review missing",
			f: &fakeMergeAPI{checks: [][]map[string]any{greenChecks}, states: []string{"blocked"},
				protection: map[string]any{"required_pull_request_reviews": map[string]any{"required_approving_review_count": 1}}},
			wantErr: "a required review is missing (0 of 1 approvals)",
		},
		{
			name:    "conflicts",
			f:       &fakeMergeAPI{checks: [][]map[string]any{greenChecks}, states: []string{"dirty"}},
			wantErr: "merge conflicts",
		},
		{
			name:    "GitHub refuses",
			f:       &fakeMergeAPI{checks: [][]map[string]any{greenChecks}, mergeCode: http.StatusMethodNotAllowed, mergeMsg: "At least 1 approving review is required"},
			wantErr: "GitHub refused the merge: At least 1 approving review is required; the PR stays open",
		},
		{
			name: "required check not reported",
			f: &fakeMergeAPI{checks: [][]map[string]any{greenChecks},
				protection: map[string]any{"required_status_checks": map[string]any{"contexts": []string{"Verify-Claim-Deletion"}}}},
			wantErr: "Verify-Claim-Deletion: expected",
		},
		{
			name:    "no terminal, no --yes",
			f:       &fakeMergeAPI{checks: [][]map[string]any{greenChecks}},
			cfg:     MergeConfig{Yes: false},
			wantErr: "pass --yes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.f.prs = map[int]map[string]any{9: fakePR(9, "delete-claim/dev7-vm", "Delete claim: NativeVsphereVM/dev7-vm")}
			tc.f.install(t)
			cfg := tc.cfg
			cfg.Arg, cfg.Repo = "9", "o/r"
			if tc.name != "no terminal, no --yes" {
				cfg.Yes = true
			}
			var out bytes.Buffer
			err := runMerge(context.Background(), &cfg, &out)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q\n%s", err, tc.wantErr, out.String())
			}
			if tc.f.merged != nil {
				t.Errorf("merged although refused")
			}
			if tc.f.called("DELETE ") {
				t.Errorf("deleted the branch although refused")
			}
		})
	}
}

func TestMergeWaitsForChecks(t *testing.T) {
	f := &fakeMergeAPI{
		prs: map[int]map[string]any{3: fakePR(3, "vm-dev7", "feat(machinery): VM dev7 (dev, labda) (MERGE = BAU)")},
		checks: [][]map[string]any{
			nil, // nothing queued yet
			{checkRun("validate-artifacts", "queued", "")},
			{checkRun("validate-artifacts", "in_progress", ""), checkRun("Verify", "completed", "failure")},
			greenChecks,
		},
		// Verify fails but is not required: only the required check counts
		protection: map[string]any{"required_status_checks": map[string]any{"checks": []map[string]any{{"context": "validate-artifacts"}}}},
		states:     []string{"unknown", "unknown", "unknown", "unknown", "unstable"},
	}
	f.install(t)
	var out bytes.Buffer
	if err := runMerge(context.Background(), &MergeConfig{Arg: "https://github.com/o/r/pull/3", Wait: true, Yes: true}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if f.merged == nil || f.checkRead < 4 {
		t.Errorf("merged = %v after %d check reads", f.merged, f.checkRead)
	}
	if !strings.Contains(out.String(), "Required: validate-artifacts") || !strings.Contains(out.String(), "Waiting for 1 check: validate-artifacts") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestMergeOnlyClaimsPRs(t *testing.T) {
	fork := fakePR(5, "vm-dev7", "x")
	fork["head"].(map[string]any)["repo"] = map[string]any{"full_name": "evil/r"}
	other := fakePR(6, "feat/foo", "feat: foo")
	intoRelease := fakePR(7, "vm-dev7", "x")
	intoRelease["base"].(map[string]any)["ref"] = "release"
	closed := fakePR(8, "vm-dev7", "x")
	closed["state"] = "closed"

	f := &fakeMergeAPI{prs: map[int]map[string]any{5: fork, 6: other, 7: intoRelease, 8: closed}, checks: [][]map[string]any{greenChecks}}
	f.install(t)
	for arg, want := range map[string]string{
		"5": "comes from evil/r", "6": "is not a claims PR", "7": "not into the default branch main", "8": "is closed",
		"foo": "no open PR on vm-foo, change-claim/foo",
	} {
		err := runMerge(context.Background(), &MergeConfig{Arg: arg, Repo: "o/r", Yes: true}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", arg, err, want)
		}
	}
	if f.merged != nil {
		t.Errorf("merged a PR claims does not recognise")
	}
	// The PR claims opened itself in this run (--merge) may be on any branch.
	if err := runMerge(context.Background(), &MergeConfig{Arg: "6", Repo: "o/r", Yes: true, created: true}, &bytes.Buffer{}); err != nil {
		t.Errorf("created PR: %v", err)
	}
}

func TestMergeAmbiguousName(t *testing.T) {
	f := &fakeMergeAPI{prs: map[int]map[string]any{
		1: fakePR(1, "vm-dev7", "order"), 2: fakePR(2, "delete-claim/dev7-vm", "delete"),
	}}
	f.install(t)
	err := runMerge(context.Background(), &MergeConfig{Arg: "dev7", Repo: "o/r", Yes: true}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "matches 2 open PRs") || !strings.Contains(err.Error(), "#2 delete") {
		t.Errorf("err = %v", err)
	}
}

func TestMergeAsksAndDryRun(t *testing.T) {
	f := &fakeMergeAPI{prs: map[int]map[string]any{1: fakePR(1, "vm-dev7", "feat(machinery): VM dev7 (MERGE = BAU)")}, checks: [][]map[string]any{greenChecks}}
	f.install(t)

	if err := runMerge(context.Background(), &MergeConfig{Arg: "1", Repo: "o/r", DryRun: true}, &bytes.Buffer{}); err != nil || f.merged != nil {
		t.Fatalf("dry run: err = %v, merged = %v", err, f.merged)
	}

	stdinIsTerminal = func() bool { return true }
	var asked string
	confirmMerge = func(title, consequence string) (bool, error) {
		asked = title + " | " + consequence
		return false, nil
	}
	var out bytes.Buffer
	if err := runMerge(context.Background(), &MergeConfig{Arg: "1", Repo: "o/r"}, &out); err != nil {
		t.Fatal(err)
	}
	if f.merged != nil || !strings.Contains(out.String(), "Cancelled") {
		t.Errorf("merged after a No:\n%s", out.String())
	}
	if !strings.Contains(asked, `Merge PR #1 "feat(machinery): VM dev7 (MERGE = BAU)"?`) || !strings.Contains(asked, "MERGE = BAU: merging builds dev7") {
		t.Errorf("asked %q", asked)
	}
}

func decodePR(t *testing.T, m map[string]any) *github.PullRequest {
	t.Helper()
	b, _ := json.Marshal(m)
	pr := new(github.PullRequest)
	if err := json.Unmarshal(b, pr); err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestChangeMergeNeedsWait(t *testing.T) {
	err := runChange(&ChangeConfig{Name: "dev7-vm", Size: "large", Merge: true, NoWait: true}, false)
	if err == nil || !strings.Contains(err.Error(), "drop --no-wait") {
		t.Errorf("err = %v", err)
	}
}

func TestChangeMergeRefusesRunID(t *testing.T) {
	for _, cfg := range []*ChangeConfig{
		{Name: "dev7-vm", Retry: true, Merge: true},
		{Name: "dev7-vm", RunID: "fix-docker", Merge: true},
	} {
		err := runChange(cfg, false)
		if err == nil || !strings.Contains(err.Error(), "--merge does not go with --retry/--run-id") || !strings.Contains(err.Error(), "claims merge") {
			t.Errorf("%+v: err = %v", cfg, err)
		}
	}
}
