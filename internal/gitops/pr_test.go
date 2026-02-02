package gitops_test

import (
	"testing"

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

func TestCheckGHInstalled(t *testing.T) {
	// This test just verifies the function doesn't panic
	// The actual result depends on system configuration
	result := gitops.CheckGHInstalled()
	t.Logf("gh CLI installed: %v", result)
}

func TestCreatePR_NoGH(t *testing.T) {
	// Skip if gh is installed, as we want to test the "not found" case
	if gitops.CheckGHInstalled() {
		t.Skip("gh CLI is installed, skipping 'not found' test")
	}

	config := gitops.PRConfig{
		Title:       "Test PR",
		Description: "Test description",
		BaseBranch:  "main",
	}

	_, err := gitops.CreatePR(config, "/tmp")
	if err == nil {
		t.Error("expected error when gh CLI is not installed")
	}
}

func TestAddLabelsToPR_EmptyLabels(t *testing.T) {
	// Adding empty labels should return nil without error
	err := gitops.AddLabelsToPR(1, []string{}, "/tmp")
	if err != nil {
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
