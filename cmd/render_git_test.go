package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stuttgart-things/claims/internal/registry"
)

func TestExtractRepoSlug(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "HTTPS URL with .git suffix",
			url:      "https://github.com/owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "HTTPS URL without .git suffix",
			url:      "https://github.com/owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "SSH URL with .git suffix",
			url:      "git@github.com:owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "SSH URL without .git suffix",
			url:      "git@github.com:owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "HTTPS with organization path",
			url:      "https://gitlab.com/org/subgroup/repo.git",
			expected: "subgroup/repo",
		},
		{
			name:     "SSH with nested path",
			url:      "git@gitlab.com:org/repo.git",
			expected: "org/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractRepoSlug(tt.url)
			if result != tt.expected {
				t.Errorf("extractRepoSlug(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

func TestFindRepoRoot(t *testing.T) {
	t.Run("finds root from nested directory", func(t *testing.T) {
		repoRoot := t.TempDir()

		// Create .git directory to simulate a git repo
		gitDir := filepath.Join(repoRoot, ".git")
		if err := os.Mkdir(gitDir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create nested directory
		nested := filepath.Join(repoRoot, "a", "b", "c")
		if err := os.MkdirAll(nested, 0755); err != nil {
			t.Fatal(err)
		}

		found, err := findRepoRoot(nested)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found != repoRoot {
			t.Errorf("expected %q, got %q", repoRoot, found)
		}
	})

	t.Run("finds root from repo root itself", func(t *testing.T) {
		repoRoot := t.TempDir()

		gitDir := filepath.Join(repoRoot, ".git")
		if err := os.Mkdir(gitDir, 0755); err != nil {
			t.Fatal(err)
		}

		found, err := findRepoRoot(repoRoot)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found != repoRoot {
			t.Errorf("expected %q, got %q", repoRoot, found)
		}
	})

	t.Run("errors when not in a git repo", func(t *testing.T) {
		noGitDir := t.TempDir()

		_, err := findRepoRoot(noGitDir)
		if err == nil {
			t.Fatal("expected error but got none")
		}
		if err.Error() != "not a git repository" {
			t.Errorf("expected 'not a git repository' error, got %q", err.Error())
		}
	})
}

func TestUpdateRegistryForRender(t *testing.T) {
	t.Run("creates registry file if it does not exist", func(t *testing.T) {
		repoRoot := t.TempDir()

		// Create .git directory
		if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		// Create output directory inside claims/
		outputDir := filepath.Join(repoRoot, "claims", "infra")
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			t.Fatal(err)
		}

		// Write a rendered file so OutputPath is valid
		outputFile := filepath.Join(outputDir, "my-vm", "claim.yaml")
		if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outputFile, []byte("kind: Claim"), 0644); err != nil {
			t.Fatal(err)
		}

		results := []RenderResult{
			{
				TemplateName: "vsphere-vm",
				ResourceName: "my-vm",
				OutputPath:   outputFile,
				Content:      "kind: Claim",
			},
		}

		config := &RenderConfig{
			OutputDir: outputDir,
			GitConfig: &GitConfig{
				User: "testuser",
			},
		}

		updateRegistryForRender(results, config)

		// Verify registry was created
		registryPath := filepath.Join(repoRoot, "claims", "registry.yaml")
		reg, err := registry.Load(registryPath)
		if err != nil {
			t.Fatalf("registry should have been created: %v", err)
		}

		entry := registry.FindEntry(reg, "my-vm")
		if entry == nil {
			t.Fatal("registry should contain my-vm entry")
		}
		if entry.Template != "vsphere-vm" {
			t.Errorf("expected template vsphere-vm, got %s", entry.Template)
		}
		if entry.Category != "infra" {
			t.Errorf("expected category infra, got %s", entry.Category)
		}
		if entry.CreatedBy != "testuser" {
			t.Errorf("expected createdBy testuser, got %s", entry.CreatedBy)
		}
		if entry.Status != "active" {
			t.Errorf("expected status active, got %s", entry.Status)
		}
	})

	t.Run("adds entries to existing registry", func(t *testing.T) {
		repoRoot := t.TempDir()

		// Create .git directory
		if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		// Create existing registry with one entry
		claimsDir := filepath.Join(repoRoot, "claims")
		if err := os.MkdirAll(claimsDir, 0755); err != nil {
			t.Fatal(err)
		}
		reg := registry.NewRegistry()
		registry.AddEntry(reg, registry.ClaimEntry{
			Name:     "existing-vm",
			Template: "vsphere-vm",
			Category: "infra",
			Status:   "active",
		})
		registryPath := filepath.Join(claimsDir, "registry.yaml")
		if err := registry.Save(registryPath, reg); err != nil {
			t.Fatal(err)
		}

		// Create output directory
		outputDir := filepath.Join(claimsDir, "apps")
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			t.Fatal(err)
		}

		outputFile := filepath.Join(outputDir, "my-db", "claim.yaml")
		if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outputFile, []byte("kind: Claim"), 0644); err != nil {
			t.Fatal(err)
		}

		results := []RenderResult{
			{
				TemplateName: "postgres",
				ResourceName: "my-db",
				OutputPath:   outputFile,
				Content:      "kind: Claim",
			},
		}

		config := &RenderConfig{
			OutputDir: outputDir,
		}

		updateRegistryForRender(results, config)

		// Verify both entries exist
		reg, err := registry.Load(registryPath)
		if err != nil {
			t.Fatal(err)
		}
		if registry.FindEntry(reg, "existing-vm") == nil {
			t.Error("registry should still contain existing-vm entry")
		}
		if registry.FindEntry(reg, "my-db") == nil {
			t.Error("registry should contain my-db entry")
		}
	})

	t.Run("skips failed results", func(t *testing.T) {
		repoRoot := t.TempDir()

		// Create .git directory
		if err := os.Mkdir(filepath.Join(repoRoot, ".git"), 0755); err != nil {
			t.Fatal(err)
		}

		// Create output directory
		outputDir := filepath.Join(repoRoot, "claims", "infra")
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			t.Fatal(err)
		}

		successFile := filepath.Join(outputDir, "good-vm", "claim.yaml")
		if err := os.MkdirAll(filepath.Dir(successFile), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(successFile, []byte("kind: Claim"), 0644); err != nil {
			t.Fatal(err)
		}

		results := []RenderResult{
			{
				TemplateName: "vsphere-vm",
				ResourceName: "good-vm",
				OutputPath:   successFile,
				Content:      "kind: Claim",
			},
			{
				TemplateName: "failed-template",
				ResourceName: "bad-vm",
				OutputPath:   "",
				Error:        &testError{},
			},
		}

		config := &RenderConfig{
			OutputDir: outputDir,
		}

		updateRegistryForRender(results, config)

		registryPath := filepath.Join(repoRoot, "claims", "registry.yaml")
		reg, err := registry.Load(registryPath)
		if err != nil {
			t.Fatalf("registry should have been created: %v", err)
		}

		if registry.FindEntry(reg, "good-vm") == nil {
			t.Error("registry should contain good-vm entry")
		}
		if registry.FindEntry(reg, "bad-vm") != nil {
			t.Error("registry should not contain bad-vm entry")
		}
	})

	t.Run("does nothing when not in a git repo", func(t *testing.T) {
		noGitDir := t.TempDir()
		outputDir := filepath.Join(noGitDir, "claims", "infra")
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			t.Fatal(err)
		}

		results := []RenderResult{
			{
				TemplateName: "vsphere-vm",
				ResourceName: "my-vm",
				OutputPath:   filepath.Join(outputDir, "my-vm.yaml"),
				Content:      "kind: Claim",
			},
		}

		config := &RenderConfig{
			OutputDir: outputDir,
		}

		// Should not panic or create registry
		updateRegistryForRender(results, config)

		registryPath := filepath.Join(noGitDir, "claims", "registry.yaml")
		if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
			t.Error("registry should not be created outside a git repo")
		}
	})
}
