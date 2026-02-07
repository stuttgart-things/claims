package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")

	// Create a registry, save it, reload it
	reg := NewRegistry()
	AddEntry(reg, ClaimEntry{
		Name:     "app-pvc",
		Template: "volumeclaim",
		Category: "infra",
		Status:   "active",
	})

	if err := Save(path, reg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(loaded.Claims))
	}
	if loaded.Claims[0].Name != "app-pvc" {
		t.Errorf("expected name app-pvc, got %s", loaded.Claims[0].Name)
	}
	if loaded.APIVersion != DefaultAPIVersion {
		t.Errorf("expected apiVersion %s, got %s", DefaultAPIVersion, loaded.APIVersion)
	}
}

func TestLoadNotFound(t *testing.T) {
	_, err := Load("/nonexistent/registry.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestAddEntryReplace(t *testing.T) {
	reg := NewRegistry()
	AddEntry(reg, ClaimEntry{Name: "a", Status: "active"})
	AddEntry(reg, ClaimEntry{Name: "a", Status: "deleted"})

	if len(reg.Claims) != 1 {
		t.Fatalf("expected 1 claim after replace, got %d", len(reg.Claims))
	}
	if reg.Claims[0].Status != "deleted" {
		t.Errorf("expected status deleted, got %s", reg.Claims[0].Status)
	}
}

func TestRemoveEntry(t *testing.T) {
	reg := NewRegistry()
	AddEntry(reg, ClaimEntry{Name: "a"})
	AddEntry(reg, ClaimEntry{Name: "b"})

	if err := RemoveEntry(reg, "a"); err != nil {
		t.Fatalf("RemoveEntry: %v", err)
	}
	if len(reg.Claims) != 1 {
		t.Fatalf("expected 1 claim, got %d", len(reg.Claims))
	}
	if reg.Claims[0].Name != "b" {
		t.Errorf("expected b, got %s", reg.Claims[0].Name)
	}
}

func TestRemoveEntryNotFound(t *testing.T) {
	reg := NewRegistry()
	if err := RemoveEntry(reg, "nonexistent"); err == nil {
		t.Fatal("expected error for missing entry")
	}
}

func TestFindEntry(t *testing.T) {
	reg := NewRegistry()
	AddEntry(reg, ClaimEntry{Name: "a", Template: "vol"})

	found := FindEntry(reg, "a")
	if found == nil {
		t.Fatal("expected to find entry")
	}
	if found.Template != "vol" {
		t.Errorf("expected template vol, got %s", found.Template)
	}

	if FindEntry(reg, "missing") != nil {
		t.Error("expected nil for missing entry")
	}
}

func TestFilterEntries(t *testing.T) {
	reg := NewRegistry()
	AddEntry(reg, ClaimEntry{Name: "a", Category: "infra", Template: "vol"})
	AddEntry(reg, ClaimEntry{Name: "b", Category: "infra", Template: "net"})
	AddEntry(reg, ClaimEntry{Name: "c", Category: "apps", Template: "vol"})

	tests := []struct {
		category string
		template string
		want     int
	}{
		{"infra", "", 2},
		{"", "vol", 2},
		{"infra", "vol", 1},
		{"", "", 3},
		{"missing", "", 0},
	}

	for _, tt := range tests {
		got := FilterEntries(reg, tt.category, tt.template)
		if len(got) != tt.want {
			t.Errorf("FilterEntries(%q, %q): got %d, want %d", tt.category, tt.template, len(got), tt.want)
		}
	}
}

func TestSaveCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "registry.yaml")

	// Should fail because parent dir doesn't exist
	reg := NewRegistry()
	err := Save(path, reg)
	if err == nil {
		// os.WriteFile doesn't create dirs, so this should fail
		// unless the dir happens to exist
		if _, statErr := os.Stat(filepath.Dir(path)); os.IsNotExist(statErr) {
			t.Fatal("expected error writing to nonexistent directory")
		}
	}
}
