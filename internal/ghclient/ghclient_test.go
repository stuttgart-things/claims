package ghclient_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/ghclient"
)

func stubGH(t *testing.T, token string) {
	t.Helper()
	prev := ghclient.GHAuthToken
	ghclient.GHAuthToken = func() (string, error) {
		if token == "" {
			return "", errors.New("not logged in")
		}
		return token, nil
	}
	t.Cleanup(func() { ghclient.GHAuthToken = prev })
}

func TestToken(t *testing.T) {
	for _, tc := range []struct {
		name, flag, git, github, gh, want string
	}{
		{name: "flag first", flag: "f", git: "g1", github: "g2", gh: "g3", want: "f"},
		{name: "GIT_TOKEN", git: "g1", github: "g2", gh: "g3", want: "g1"},
		{name: "GITHUB_TOKEN", github: "g2", gh: "g3", want: "g2"},
		{name: "gh login", gh: "g3", want: "g3"},
		{name: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_TOKEN", tc.git)
			t.Setenv("GITHUB_TOKEN", tc.github)
			stubGH(t, tc.gh)
			got, err := ghclient.Token(tc.flag)
			if tc.want == "" {
				if !errors.Is(err, ghclient.ErrNoToken) {
					t.Fatalf("err = %v, want ErrNoToken", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("token = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestNewUsesAPIURL(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"login":"jdoe"}`))
	}))
	defer srv.Close()
	t.Setenv(ghclient.EnvAPIURL, srv.URL)
	c, err := ghclient.New("tok")
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := c.Users.Get(context.Background(), "")
	if err != nil || u.GetLogin() != "jdoe" || auth != "Bearer tok" {
		t.Errorf("user = %v, %v, auth %q", u, err, auth)
	}
}

func TestRepoFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/fleet.git":   "acme/fleet",
		"https://github.com/acme/fleet":       "acme/fleet",
		"https://x:tok@github.com/acme/fleet": "acme/fleet",
		"git@github.com:acme/fleet.git":       "acme/fleet",
		"ssh://git@github.com/acme/fleet.git": "acme/fleet",
		"/srv/git/fleet":                      "",
		"https://github.com/acme":             "",
	} {
		got, err := ghclient.RepoFromURL(in)
		if (want == "") != (err != nil) || got != want {
			t.Errorf("RepoFromURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestSplitRepo(t *testing.T) {
	for in, ok := range map[string]bool{"a/b": true, "a": false, "/b": false, "a/": false, "a/b/c": false} {
		if _, _, err := ghclient.SplitRepo(in); (err == nil) != ok {
			t.Errorf("SplitRepo(%q) err = %v", in, err)
		}
	}
}

func TestDescribe(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"message":"Bad credentials"}`, "token was refused"},
		{403, `{"message":"Resource not accessible by integration"}`, "Resource not accessible"},
		{404, `{"message":"Not Found"}`, "private repositories answer 404"},
		{422, `{"message":"Validation Failed","errors":[{"message":"A pull request already exists for acme:b."}]}`, "already exists"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		t.Setenv(ghclient.EnvAPIURL, srv.URL)
		c, _ := ghclient.New("tok")
		_, _, err := c.Users.Get(context.Background(), "")
		err = ghclient.Describe(err, "doing it")
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.HasPrefix(err.Error(), "doing it: ") {
			t.Errorf("%d: %v, want %q", tc.status, err, tc.want)
		}
		srv.Close()
	}
}
