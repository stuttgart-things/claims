// Package ghclient builds the go-github client claims talks to GitHub with
// (claims#108): the token, the API endpoint and errors that say what to do.
// The gh CLI is optional -- only a fallback for the token.
package ghclient

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"
)

// EnvAPIURL points the client at another API endpoint (GitHub Enterprise;
// tests). GitHub Actions sets it to https://api.github.com.
const EnvAPIURL = "GITHUB_API_URL"

// ErrNoToken says how to give claims a token.
var ErrNoToken = errors.New("no GitHub token: set GITHUB_TOKEN (or GIT_TOKEN), pass --git-token where the command has it, or log in with `gh auth login`")

// GHAuthToken asks the gh CLI for its token; a variable for tests.
var GHAuthToken = func() (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", err
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	return strings.TrimSpace(string(out)), err
}

// Token is explicit (a --git-token flag), else $GIT_TOKEN, $GITHUB_TOKEN, else
// gh's login. There is no anonymous fallback: the fleet repository is private.
func Token(explicit string) (string, error) {
	for _, t := range []string{explicit, os.Getenv("GIT_TOKEN"), os.Getenv("GITHUB_TOKEN")} {
		if t != "" {
			return t, nil
		}
	}
	if t, err := GHAuthToken(); err == nil && t != "" {
		return t, nil
	}
	return "", ErrNoToken
}

// New is a client with token, against $GITHUB_API_URL when set.
func New(token string) (*github.Client, error) {
	opts := []github.ClientOptionsFunc{github.WithAuthToken(token), github.WithTimeout(30 * time.Second)}
	if u := os.Getenv(EnvAPIURL); u != "" && strings.TrimRight(u, "/") != "https://api.github.com" {
		if !strings.HasSuffix(u, "/") {
			u += "/"
		}
		opts = append(opts, github.WithURLs(&u, &u))
	}
	return github.NewClient(opts...)
}

// FromEnv is New(Token(explicit)).
func FromEnv(explicit string) (*github.Client, error) {
	token, err := Token(explicit)
	if err != nil {
		return nil, err
	}
	return New(token)
}

// SplitRepo splits "owner/name".
func SplitRepo(ownerRepo string) (owner, repo string, err error) {
	owner, repo, ok := strings.Cut(ownerRepo, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("%q is not owner/name", ownerRepo)
	}
	return owner, repo, nil
}

// RepoFromURL is the owner/name of a git remote URL: https://host/o/r(.git),
// git@host:o/r(.git) or ssh://git@host/o/r(.git).
func RepoFromURL(url string) (string, error) {
	p := strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	switch {
	case strings.Contains(p, "://"):
		p = p[strings.Index(p, "://")+3:]
		if i := strings.Index(p, "/"); i >= 0 {
			p = p[i+1:]
		} else {
			p = ""
		}
	case strings.Contains(p, ":"):
		p = p[strings.Index(p, ":")+1:]
	default:
		p = ""
	}
	if _, _, err := SplitRepo(p); err != nil {
		return "", fmt.Errorf("remote %q does not name a GitHub repository owner/name", url)
	}
	return p, nil
}

// Describe turns the API's 401/403/404/422 into what to do about it. action
// says what failed ("starting dispatch-change-claim.yaml in o/r").
func Describe(err error, action string) error {
	var gerr *github.ErrorResponse
	if errors.As(err, &gerr) && gerr.Response != nil {
		switch gerr.Response.StatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("%s: the token was refused (401); check GITHUB_TOKEN / GIT_TOKEN or `gh auth status`", action)
		case http.StatusForbidden:
			return fmt.Errorf("%s: forbidden (403: the token lacks the permission, or a rate limit): %s", action, gerr.Message)
		case http.StatusNotFound:
			return fmt.Errorf("%s: not found (404) -- it does not exist, or the token cannot see it (private repositories answer 404)", action)
		case http.StatusUnprocessableEntity:
			msg := gerr.Message
			for _, e := range gerr.Errors {
				if e.Message != "" {
					msg += ": " + e.Message
				}
			}
			return fmt.Errorf("%s: %s", action, msg)
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}
