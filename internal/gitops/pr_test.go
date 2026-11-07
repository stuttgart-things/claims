package gitops_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/config"

	"github.com/stuttgart-things/claims/internal/ghclient"

	"github.com/stuttgart-things/claims/internal/gitops"
)

func TestPRConfig(t *testing.T) {
	config := gitops.PRConfig{
		Title:       "Test PR",
		Description: "This is a test PR",
		Labels:      []string{"test", "automated"},
		BaseBranch:  "main",
		HeadBranch:  "feature/test",
	}

	if config.Title != "Test PR" {
		t.Errorf("unexpected Title: %s", config.Title)
	}
	if config.Description != "This is a test PR" {
		t.Errorf("unexpected Description: %s", config.Description)
	}
	if len(config.Labels) != 2 {
		t.Errorf("unexpected Labels count: %d", len(config.Labels))
	}
	if config.BaseBranch != "main" {
		t.Errorf("unexpected BaseBranch: %s", config.BaseBranch)
	}
	if config.HeadBranch != "feature/test" {
		t.Errorf("unexpected HeadBranch: %s", config.HeadBranch)
	}
}

func TestPRResult(t *testing.T) {
	result := gitops.PRResult{
		Number: 42,
		URL:    "https://github.com/test/repo/pull/42",
	}

	if result.Number != 42 {
		t.Errorf("unexpected Number: %d", result.Number)
	}
	if result.URL != "https://github.com/test/repo/pull/42" {
		t.Errorf("unexpected URL: %s", result.URL)
	}
}

// fakePRAPI answers POST /repos/acme/fleet/pulls and .../issues/7/labels.
type fakePRAPI struct {
	labelStatus int
	pr          map[string]any
	labels      []string
}

func (f *fakePRAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/fleet/pulls":
		_ = json.NewDecoder(r.Body).Decode(&f.pr)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.com/acme/fleet/pull/7"}`))
	case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/fleet/issues/7/labels":
		if f.labelStatus != 0 {
			w.WriteHeader(f.labelStatus)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&f.labels)
		_, _ = w.Write([]byte(`[]`))
	default:
		http.NotFound(w, r)
	}
}

// prRepo is a checkout on branch feat/x whose origin is acme/fleet on GitHub.
func prRepo(t *testing.T, f *fakePRAPI) string {
	t.Helper()
	dir := initTestRepo(t)
	g, err := gitops.New(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.CreateBranch("feat/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.GetRepo().CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{"https://github.com/acme/fleet.git"}}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	t.Setenv(ghclient.EnvAPIURL, srv.URL)
	t.Setenv("GIT_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	prev := ghclient.GHAuthToken
	ghclient.GHAuthToken = func() (string, error) { return "", errors.New("no gh in tests") }
	t.Cleanup(func() { ghclient.GHAuthToken = prev })
	return dir
}

func TestCreatePR(t *testing.T) {
	f := &fakePRAPI{}
	dir := prRepo(t, f)
	res, err := gitops.CreatePR(gitops.PRConfig{
		Title: "T", Description: "D", BaseBranch: "main", Labels: []string{"", "claims"}, Token: "tok",
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Number != 7 || res.URL != "https://github.com/acme/fleet/pull/7" {
		t.Errorf("res = %+v", res)
	}
	if f.pr["head"] != "feat/x" || f.pr["base"] != "main" || f.pr["title"] != "T" || f.pr["body"] != "D" {
		t.Errorf("PR = %v (head defaults to the current branch)", f.pr)
	}
	if len(f.labels) != 1 || f.labels[0] != "claims" {
		t.Errorf("labels = %v", f.labels)
	}
}

func TestCreatePRLabelsFail(t *testing.T) {
	f := &fakePRAPI{labelStatus: http.StatusForbidden}
	dir := prRepo(t, f)
	res, err := gitops.CreatePR(gitops.PRConfig{Title: "T", BaseBranch: "main", HeadBranch: "b", Labels: []string{"x"}, Token: "tok"}, dir)
	if res == nil || err == nil || !strings.Contains(err.Error(), res.URL) {
		t.Errorf("res = %v, err = %v: want the PR and an error naming it", res, err)
	}
}

func TestCreatePRNoToken(t *testing.T) {
	dir := prRepo(t, &fakePRAPI{})
	if _, err := gitops.CreatePR(gitops.PRConfig{Title: "T", BaseBranch: "main"}, dir); !errors.Is(err, ghclient.ErrNoToken) {
		t.Errorf("err = %v", err)
	}
	if err := gitops.CheckGitHubAuth(""); !errors.Is(err, ghclient.ErrNoToken) {
		t.Errorf("CheckGitHubAuth = %v", err)
	}
	if err := gitops.CheckGitHubAuth("tok"); err != nil {
		t.Errorf("CheckGitHubAuth(tok) = %v", err)
	}
}

func TestAddLabelsToPR_EmptyLabels(t *testing.T) {
	// Nothing to add: no repository, token or API needed.
	if err := gitops.AddLabelsToPR(1, []string{""}, "/nonexistent"); err != nil {
		t.Errorf("AddLabelsToPR with empty labels should not error: %v", err)
	}
}

func TestPRConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		config gitops.PRConfig
		valid  bool
	}{
		{
			name: "full config",
			config: gitops.PRConfig{
				Title:       "Add new feature",
				Description: "This PR adds a new feature",
				Labels:      []string{"enhancement"},
				BaseBranch:  "main",
				HeadBranch:  "feature/new",
			},
			valid: true,
		},
		{
			name: "minimal config",
			config: gitops.PRConfig{
				Title:      "Fix bug",
				BaseBranch: "main",
			},
			valid: true,
		},
		{
			name: "config with empty labels filtered",
			config: gitops.PRConfig{
				Title:      "Test PR",
				Labels:     []string{"", "valid-label", ""},
				BaseBranch: "main",
			},
			valid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Basic validation - Title and BaseBranch should be set
			hasTitle := tt.config.Title != ""
			hasBase := tt.config.BaseBranch != ""

			if (hasTitle && hasBase) != tt.valid {
				t.Errorf("config validation mismatch for %s", tt.name)
			}
		})
	}
}

func TestFilterEmptyLabels(t *testing.T) {
	labels := []string{"", "label1", "", "label2", ""}

	var filtered []string
	for _, label := range labels {
		if label != "" {
			filtered = append(filtered, label)
		}
	}

	if len(filtered) != 2 {
		t.Errorf("expected 2 non-empty labels, got %d", len(filtered))
	}
	if filtered[0] != "label1" || filtered[1] != "label2" {
		t.Errorf("unexpected filtered labels: %v", filtered)
	}
}
