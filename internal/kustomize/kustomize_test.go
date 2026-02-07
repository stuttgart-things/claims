package kustomize

import (
	"path/filepath"
	"testing"
)

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")

	k := &Kustomization{
		APIVersion: "kustomize.config.k8s.io/v1beta1",
		Kind:       "Kustomization",
		Resources:  []string{"app-pvc", "db-pvc"},
	}

	if err := Save(path, k); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(loaded.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(loaded.Resources))
	}
	if loaded.Resources[0] != "app-pvc" {
		t.Errorf("expected app-pvc, got %s", loaded.Resources[0])
	}
}

func TestAddResource(t *testing.T) {
	k := &Kustomization{Resources: []string{"a"}}

	AddResource(k, "b")
	if len(k.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(k.Resources))
	}

	// Adding duplicate should be a no-op
	AddResource(k, "b")
	if len(k.Resources) != 2 {
		t.Fatalf("expected 2 resources after duplicate add, got %d", len(k.Resources))
	}
}

func TestRemoveResource(t *testing.T) {
	k := &Kustomization{Resources: []string{"a", "b", "c"}}

	if err := RemoveResource(k, "b"); err != nil {
		t.Fatalf("RemoveResource: %v", err)
	}
	if len(k.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(k.Resources))
	}
	if k.Resources[0] != "a" || k.Resources[1] != "c" {
		t.Errorf("unexpected resources: %v", k.Resources)
	}
}

func TestRemoveResourceNotFound(t *testing.T) {
	k := &Kustomization{Resources: []string{"a"}}
	if err := RemoveResource(k, "missing"); err == nil {
		t.Fatal("expected error for missing resource")
	}
}

func TestLoadNotFound(t *testing.T) {
	_, err := Load("/nonexistent/kustomization.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
