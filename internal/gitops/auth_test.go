package gitops_test

import (
	"os"
	"testing"

	"github.com/stuttgart-things/claims/internal/gitops"
)

func TestResolveCredentials(t *testing.T) {
	// Save original env vars
	origUser := os.Getenv("GIT_USER")
	origToken := os.Getenv("GIT_TOKEN")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		os.Setenv("GIT_USER", origUser)
		os.Setenv("GIT_TOKEN", origToken)
		os.Setenv("GITHUB_TOKEN", origGitHub)
	}()

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
			name:    "missing user",
			user:    "",
			token:   "flagtoken",
			wantErr: true,
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
			// Clear env vars
			os.Unsetenv("GIT_USER")
			os.Unsetenv("GIT_TOKEN")
			os.Unsetenv("GITHUB_TOKEN")

			// Set test env vars if specified
			if tt.envUser != "" {
				os.Setenv("GIT_USER", tt.envUser)
			}
			if tt.envToken != "" {
				os.Setenv("GIT_TOKEN", tt.envToken)
			}
			if tt.envGitHub != "" {
				os.Setenv("GITHUB_TOKEN", tt.envGitHub)
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
	// Save original env vars
	origUser := os.Getenv("GIT_USER")
	origToken := os.Getenv("GIT_TOKEN")
	origGitHub := os.Getenv("GITHUB_TOKEN")
	defer func() {
		os.Setenv("GIT_USER", origUser)
		os.Setenv("GIT_TOKEN", origToken)
		os.Setenv("GITHUB_TOKEN", origGitHub)
	}()

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
			// Clear env vars
			os.Unsetenv("GIT_USER")
			os.Unsetenv("GIT_TOKEN")
			os.Unsetenv("GITHUB_TOKEN")

			// Set test env vars if specified
			if tt.envUser != "" {
				os.Setenv("GIT_USER", tt.envUser)
			}
			if tt.envToken != "" {
				os.Setenv("GIT_TOKEN", tt.envToken)
			}
			if tt.envGitHub != "" {
				os.Setenv("GITHUB_TOKEN", tt.envGitHub)
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
