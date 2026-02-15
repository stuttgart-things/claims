package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/kustomize"
	"github.com/stuttgart-things/claims/internal/registry"
)

func TestPerformDelete(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		category     string
		setup        func(t *testing.T, repoRoot string)
		wantErr      bool
		errContains  string
		verify       func(t *testing.T, repoRoot string, result *DeleteResult)
	}{
		{
			name:         "deletes claim directory and updates registry and kustomization",
			resourceName: "my-vm",
			category:     "infra",
			setup: func(t *testing.T, repoRoot string) {
				t.Helper()
				// Create claim directory with a file
				claimDir := filepath.Join(repoRoot, "claims", "infra", "my-vm")
				if err := os.MkdirAll(claimDir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(claimDir, "claim.yaml"), []byte("kind: Claim"), 0644); err != nil {
					t.Fatal(err)
				}

				// Create kustomization.yaml
				k := &kustomize.Kustomization{
					APIVersion: "kustomize.config.k8s.io/v1beta1",
					Kind:       "Kustomization",
					Resources:  []string{"my-vm", "other-vm"},
				}
				kPath := filepath.Join(repoRoot, "claims", "infra", "kustomization.yaml")
				if err := kustomize.Save(kPath, k); err != nil {
					t.Fatal(err)
				}

				// Create registry.yaml
				reg := registry.NewRegistry()
				registry.AddEntry(reg, registry.ClaimEntry{
					Name:     "my-vm",
					Template: "vsphere-vm",
					Category: "infra",
					Status:   "active",
				})
				registry.AddEntry(reg, registry.ClaimEntry{
					Name:     "other-vm",
					Template: "vsphere-vm",
					Category: "infra",
					Status:   "active",
				})
				regPath := filepath.Join(repoRoot, "claims", "registry.yaml")
				if err := os.MkdirAll(filepath.Dir(regPath), 0755); err != nil {
					t.Fatal(err)
				}
				if err := registry.Save(regPath, reg); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, repoRoot string, result *DeleteResult) {
				t.Helper()
				// Claim directory should be removed
				claimDir := filepath.Join(repoRoot, "claims", "infra", "my-vm")
				if _, err := os.Stat(claimDir); !os.IsNotExist(err) {
					t.Error("claim directory should have been removed")
				}

				// Kustomization should no longer contain the resource
				k, err := kustomize.Load(filepath.Join(repoRoot, "claims", "infra", "kustomization.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range k.Resources {
					if r == "my-vm" {
						t.Error("kustomization should not contain my-vm resource")
					}
				}
				if len(k.Resources) != 1 || k.Resources[0] != "other-vm" {
					t.Errorf("expected [other-vm], got %v", k.Resources)
				}

				// Registry should no longer contain the entry
				reg, err := registry.Load(filepath.Join(repoRoot, "claims", "registry.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if registry.FindEntry(reg, "my-vm") != nil {
					t.Error("registry should not contain my-vm entry")
				}
				if registry.FindEntry(reg, "other-vm") == nil {
					t.Error("registry should still contain other-vm entry")
				}

				// Verify result
				if result.ResourceName != "my-vm" {
					t.Errorf("expected ResourceName my-vm, got %s", result.ResourceName)
				}
				if result.Category != "infra" {
					t.Errorf("expected Category infra, got %s", result.Category)
				}
				if result.Path != "claims/infra/my-vm" {
					t.Errorf("expected Path claims/infra/my-vm, got %s", result.Path)
				}
			},
		},
		{
			name:         "errors when claim directory does not exist",
			resourceName: "nonexistent",
			category:     "infra",
			setup: func(t *testing.T, repoRoot string) {
				t.Helper()
				// Create registry.yaml (required by performDelete)
				reg := registry.NewRegistry()
				regPath := filepath.Join(repoRoot, "claims", "registry.yaml")
				if err := os.MkdirAll(filepath.Dir(regPath), 0755); err != nil {
					t.Fatal(err)
				}
				if err := registry.Save(regPath, reg); err != nil {
					t.Fatal(err)
				}
			},
			wantErr:     true,
			errContains: "claim directory not found",
		},
		{
			name:         "succeeds without kustomization.yaml",
			resourceName: "my-db",
			category:     "apps",
			setup: func(t *testing.T, repoRoot string) {
				t.Helper()
				// Create claim directory
				claimDir := filepath.Join(repoRoot, "claims", "apps", "my-db")
				if err := os.MkdirAll(claimDir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(claimDir, "claim.yaml"), []byte("kind: Claim"), 0644); err != nil {
					t.Fatal(err)
				}

				// Create registry.yaml
				reg := registry.NewRegistry()
				registry.AddEntry(reg, registry.ClaimEntry{
					Name:     "my-db",
					Template: "postgres",
					Category: "apps",
					Status:   "active",
				})
				regPath := filepath.Join(repoRoot, "claims", "registry.yaml")
				if err := os.MkdirAll(filepath.Dir(regPath), 0755); err != nil {
					t.Fatal(err)
				}
				if err := registry.Save(regPath, reg); err != nil {
					t.Fatal(err)
				}
			},
			verify: func(t *testing.T, repoRoot string, result *DeleteResult) {
				t.Helper()
				// Directory should be removed
				claimDir := filepath.Join(repoRoot, "claims", "apps", "my-db")
				if _, err := os.Stat(claimDir); !os.IsNotExist(err) {
					t.Error("claim directory should have been removed")
				}
				// Registry entry should be removed
				reg, err := registry.Load(filepath.Join(repoRoot, "claims", "registry.yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if registry.FindEntry(reg, "my-db") != nil {
					t.Error("registry should not contain my-db entry")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			tt.setup(t, repoRoot)

			result, err := performDelete(repoRoot, "claims/registry.yaml", tt.resourceName, tt.category)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.verify != nil {
				tt.verify(t, repoRoot, result)
			}
		})
	}
}

func TestPrintDeleteDryRun(t *testing.T) {
	repoRoot := t.TempDir()

	// Create a claim directory to verify it is NOT removed
	claimDir := filepath.Join(repoRoot, "claims", "infra", "test-vm")
	if err := os.MkdirAll(claimDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claimDir, "claim.yaml"), []byte("kind: Claim"), 0644); err != nil {
		t.Fatal(err)
	}

	err := printDeleteDryRun("test-vm", "infra", "claims/infra/test-vm", repoRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no files were modified
	if _, err := os.Stat(claimDir); os.IsNotExist(err) {
		t.Error("dry run should not remove claim directory")
	}
	if _, err := os.Stat(filepath.Join(claimDir, "claim.yaml")); os.IsNotExist(err) {
		t.Error("dry run should not remove claim files")
	}
}

func TestResolveRepoRoot(t *testing.T) {
	tests := []struct {
		name    string
		config  *DeleteConfig
		setup   func(t *testing.T) string
		wantErr bool
	}{
		{
			name: "errors when RepoURL is set without git flags",
			config: &DeleteConfig{
				RepoURL: "https://github.com/owner/repo.git",
			},
			setup:   func(t *testing.T) string { return "" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)

			_, err := resolveRepoRoot(tt.config)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestGenerateDeletePRDescription(t *testing.T) {
	result := &DeleteResult{
		ResourceName: "my-vm",
		Category:     "infra",
		Path:         "claims/infra/my-vm",
	}

	desc := generateDeletePRDescription(result)

	// Check for expected sections and content
	checks := []struct {
		label    string
		contains string
	}{
		{"resource name", "my-vm"},
		{"category", "infra"},
		{"path", "claims/infra/my-vm"},
		{"summary heading", "## Summary"},
		{"changes heading", "## Changes"},
		{"kustomization mention", "kustomization.yaml"},
		{"registry mention", "registry.yaml"},
	}

	for _, c := range checks {
		if !strings.Contains(desc, c.contains) {
			t.Errorf("PR description should contain %s (%q), got:\n%s", c.label, c.contains, desc)
		}
	}
}
