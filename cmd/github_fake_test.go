package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stuttgart-things/claims/internal/ghclient"
)

// fakeGitHubAPI answers the GitHub API calls claims change makes, for any
// owner/repo, and records them as "METHOD /path?query".
type fakeGitHubAPI struct {
	runDetails  bool     // the dispatch returns the run id (else claims lists the runs)
	conclusion  string   // of run 42
	annotations []string // of the failed job
	login       string   // GET /user

	mu       sync.Mutex
	calls    []string
	inputs   map[string]any // of the last dispatch
	dispatch string         // its ref
}

func (f *fakeGitHubAPI) install(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv(ghclient.EnvAPIURL, srv.URL)
	t.Setenv("GITHUB_TOKEN", "tok")
	orig := changePoll
	changePoll = time.Millisecond
	t.Cleanup(func() { changePoll = orig })
}

func (f *fakeGitHubAPI) called(prefix string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	return ""
}

func (f *fakeGitHubAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	call := r.Method + " " + r.URL.Path
	if r.URL.RawQuery != "" {
		call += "?" + r.URL.RawQuery
	}
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	path := r.URL.Path
	if strings.HasPrefix(path, "/repos/") { // drop /repos/<owner>/<repo>
		parts := strings.SplitN(path, "/", 5)
		if len(parts) == 5 {
			path = "/" + parts[4]
		}
	}
	js := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.Method == http.MethodPost && path == "/actions/workflows/"+changeWorkflow+"/dispatches":
		var body struct {
			Ref    string         `json:"ref"`
			Inputs map[string]any `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.inputs, f.dispatch = body.Inputs, body.Ref
		f.mu.Unlock()
		if !f.runDetails {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		js(map[string]any{"workflow_run_id": 42, "html_url": "https://github.com/o/r/actions/runs/42"})
	case path == "/actions/workflows/"+changeWorkflow+"/runs":
		js(map[string]any{"total_count": 2, "workflow_runs": []map[string]any{
			{"id": 1, "created_at": time.Now().Add(-time.Hour), "status": "completed", "conclusion": "success"},
			{"id": 42, "created_at": time.Now(), "status": "in_progress"},
		}})
	case path == "/actions/runs/42":
		js(map[string]any{"id": 42, "status": "completed", "conclusion": f.conclusion, "html_url": "https://github.com/o/r/actions/runs/42"})
	case path == "/actions/runs/42/jobs":
		js(map[string]any{"total_count": 2, "jobs": []map[string]any{
			{"id": 7, "conclusion": "failure"}, {"id": 8, "conclusion": "success"},
		}})
	case path == "/check-runs/7/annotations":
		anns := []map[string]any{{"message": "The ubuntu-latest label will migrate", "annotation_level": "warning"}}
		for _, m := range f.annotations {
			anns = append(anns, map[string]any{"message": m, "annotation_level": "failure"})
		}
		js(anns)
	case path == "/pulls":
		js([]map[string]any{{"number": 7, "html_url": "https://github.com/o/r/pull/7"}})
	case path == "/user":
		js(map[string]any{"login": f.login})
	default:
		http.NotFound(w, r)
	}
}
