package gitops

import (
	"fmt"
	"os"
)

// ResolveCredentials gets git credentials from flags or environment
func ResolveCredentials(user, token string) (string, string, error) {
	if user == "" {
		user = os.Getenv("GIT_USER")
	}
	if token == "" {
		token = os.Getenv("GIT_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN") // GitHub Actions compatibility
		}
	}

	if user == "" || token == "" {
		return "", "", fmt.Errorf("git credentials required: set --git-user/--git-token or GIT_USER/GIT_TOKEN environment variables")
	}

	return user, token, nil
}

// ResolveCredentialsOptional gets git credentials if available, but doesn't error if missing
// This is useful for local commits that don't require push
func ResolveCredentialsOptional(user, token string) (string, string) {
	if user == "" {
		user = os.Getenv("GIT_USER")
	}
	if token == "" {
		token = os.Getenv("GIT_TOKEN")
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
	}
	return user, token
}
