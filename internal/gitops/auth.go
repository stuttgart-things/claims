package gitops

import (
	"fmt"
	"os"

	"github.com/stuttgart-things/claims/internal/ghclient"
)

// ErrNoGitToken says how to give claims a token for push and clone.
var ErrNoGitToken = fmt.Errorf("git credentials required: pass --git-token, set GIT_TOKEN or GITHUB_TOKEN, or log in with `gh auth login` (--git-user/GIT_USER is optional)")

// ResolveCredentials gets git credentials from flags or environment
func ResolveCredentials(user, token string) (string, string, error) {
	if user == "" {
		user = os.Getenv("GIT_USER")
		if user == "" {
			user = os.Getenv("GITHUB_USER")
		}
	}
	// The token is the flag, GIT_TOKEN, GITHUB_TOKEN, else the gh CLI's
	// login (as for the GitHub API). The user name is optional: without one,
	// TokenUser authenticates the token and commits are by claims-cli.
	t, err := ghclient.Token(token)
	if err != nil {
		return "", "", ErrNoGitToken
	}
	return user, t, nil
}

// ResolveCredentialsOptional gets git credentials if available, but doesn't error if missing
// This is useful for local commits that don't require push
func ResolveCredentialsOptional(user, token string) (string, string) {
	if user == "" {
		user = os.Getenv("GIT_USER")
		if user == "" {
			user = os.Getenv("GITHUB_USER")
		}
	}
	if token == "" {
		token = os.Getenv("GIT_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
	}
	return user, token
}
