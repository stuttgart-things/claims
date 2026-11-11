package gitops_test

import (
	"errors"
	"testing"

	"github.com/stuttgart-things/claims/internal/ghclient"
	"github.com/stuttgart-things/claims/internal/gitops"
)

// noGH makes the gh CLI fallback find no login.
func noGH(t *testing.T) {
	t.Helper()
	prev := ghclient.GHAuthToken
	ghclient.GHAuthToken = func() (string, error) { return "", errors.New("no gh in tests") }
	t.Cleanup(func() { ghclient.GHAuthToken = prev })
}

// The gh CLI's login is the last fallback for the token, and a token alone
// is enough (claims#114: "git credentials required" although gh was logged in).
func TestResolveCredentialsGHFallback(t *testing.T) {
	t.Setenv("GIT_USER", "")
	t.Setenv("GIT_TOKEN", "")
	t.Setenv("GITHUB_USER", "")
	t.Setenv("GITHUB_TOKEN", "")
	prev := ghclient.GHAuthToken
	ghclient.GHAuthToken = func() (string, error) { return "gh-token", nil }
	t.Cleanup(func() { ghclient.GHAuthToken = prev })

	user, token, err := gitops.ResolveCredentials("", "")
	if err != nil || user != "" || token != "gh-token" {
		t.Errorf("got %q %q %v", user, token, err)
	}
}

func TestResolveCredentials(t *testing.T) {
	tests := []struct {
		name      string
		user      string
		token     string
		envUser   string
		envToken  string
		envGitHub string
		wantUser  string
		wantToken string
		wantErr   bool
	}{
		{
			name:      "credentials from flags",
			user:      "flaguser",
			token:     "flagtoken",
			wantUser:  "flaguser",
			wantToken: "flagtoken",
			wantErr:   false,
		},
		{
			name:      "credentials from GIT env vars",
			user:      "",
			token:     "",
			envUser:   "envuser",
			envToken:  "envtoken",
			wantUser:  "envuser",
			wantToken: "envtoken",
			wantErr:   false,
		},
		{
			name:      "credentials from GITHUB_TOKEN fallback",
			user:      "",
			token:     "",
			envUser:   "envuser",
			envGitHub: "githubtoken",
			wantUser:  "envuser",
			wantToken: "githubtoken",
			wantErr:   false,
		},
		{
			name:      "flags override env vars",
			user:      "flaguser",
			token:     "flagtoken",
			envUser:   "envuser",
			envToken:  "envtoken",
			wantUser:  "flaguser",
			wantToken: "flagtoken",
			wantErr:   false,
		},
		{
			name:      "missing user is fine with a token",
			user:      "",
			token:     "flagtoken",
			wantToken: "flagtoken",
		},
		{
			name:    "missing token",
			user:    "flaguser",
			token:   "",
			wantErr: true,
		},
		{
			name:    "both missing",
			user:    "",
			token:   "",
			wantErr: true,
		},
		{
			name:     "only env user no token",
			envUser:  "envuser",
			envToken: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Isolate from host environment; t.Setenv restores after each subtest
			noGH(t)
			t.Setenv("GIT_USER", "")
			t.Setenv("GIT_TOKEN", "")
			t.Setenv("GITHUB_USER", "")
			t.Setenv("GITHUB_TOKEN", "")

			// Set test env vars if specified
			if tt.envUser != "" {
				t.Setenv("GIT_USER", tt.envUser)
			}
			if tt.envToken != "" {
				t.Setenv("GIT_TOKEN", tt.envToken)
			}
			if tt.envGitHub != "" {
				t.Setenv("GITHUB_TOKEN", tt.envGitHub)
			}

			user, token, err := gitops.ResolveCredentials(tt.user, tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveCredentials() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if user != tt.wantUser {
					t.Errorf("ResolveCredentials() user = %v, want %v", user, tt.wantUser)
				}
				if token != tt.wantToken {
					t.Errorf("ResolveCredentials() token = %v, want %v", token, tt.wantToken)
				}
			}
		})
	}
}

func TestResolveCredentialsOptional(t *testing.T) {
	tests := []struct {
		name      string
		user      string
		token     string
		envUser   string
		envToken  string
		envGitHub string
		wantUser  string
		wantToken string
	}{
		{
			name:      "credentials from flags",
			user:      "flaguser",
			token:     "flagtoken",
			wantUser:  "flaguser",
			wantToken: "flagtoken",
		},
		{
			name:      "credentials from env vars",
			user:      "",
			token:     "",
			envUser:   "envuser",
			envToken:  "envtoken",
			wantUser:  "envuser",
			wantToken: "envtoken",
		},
		{
			name:      "credentials from GITHUB_TOKEN fallback",
			user:      "",
			token:     "",
			envUser:   "envuser",
			envGitHub: "githubtoken",
			wantUser:  "envuser",
			wantToken: "githubtoken",
		},
		{
			name:      "no credentials returns empty",
			user:      "",
			token:     "",
			wantUser:  "",
			wantToken: "",
		},
		{
			name:      "partial credentials",
			user:      "flaguser",
			token:     "",
			wantUser:  "flaguser",
			wantToken: "",
		},
		{
			name:      "flags override env vars",
			user:      "flaguser",
			token:     "flagtoken",
			envUser:   "envuser",
			envToken:  "envtoken",
			wantUser:  "flaguser",
			wantToken: "flagtoken",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Isolate from host environment; t.Setenv restores after each subtest
			t.Setenv("GIT_USER", "")
			t.Setenv("GIT_TOKEN", "")
			t.Setenv("GITHUB_USER", "")
			t.Setenv("GITHUB_TOKEN", "")

			// Set test env vars if specified
			if tt.envUser != "" {
				t.Setenv("GIT_USER", tt.envUser)
			}
			if tt.envToken != "" {
				t.Setenv("GIT_TOKEN", tt.envToken)
			}
			if tt.envGitHub != "" {
				t.Setenv("GITHUB_TOKEN", tt.envGitHub)
			}

			user, token := gitops.ResolveCredentialsOptional(tt.user, tt.token)

			if user != tt.wantUser {
				t.Errorf("ResolveCredentialsOptional() user = %v, want %v", user, tt.wantUser)
			}
			if token != tt.wantToken {
				t.Errorf("ResolveCredentialsOptional() token = %v, want %v", token, tt.wantToken)
			}
		})
	}
}
