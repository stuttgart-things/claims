package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateFilename(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		info     FileInfo
		expected string
		wantErr  bool
	}{
		{
			name:    "default pattern",
			pattern: "{{.template}}-{{.name}}.yaml",
			info: FileInfo{
				TemplateName: "vsphere-vm",
				ResourceName: "my-vm",
			},
			expected: "vsphere-vm-my-vm.yaml",
		},
		{
			name:    "name only pattern",
			pattern: "{{.name}}.yaml",
			info: FileInfo{
				TemplateName: "vsphere-vm",
				ResourceName: "my-vm",
			},
			expected: "my-vm.yaml",
		},
		{
			name:    "template only pattern",
			pattern: "{{.template}}.yaml",
			info: FileInfo{
				TemplateName: "postgres-db",
				ResourceName: "test-db",
			},
			expected: "postgres-db.yaml",
		},
		{
			name:    "custom pattern with prefix",
			pattern: "claim-{{.template}}-{{.name}}.yaml",
			info: FileInfo{
				TemplateName: "redis",
				ResourceName: "cache",
			},
			expected: "claim-redis-cache.yaml",
		},
		{
			name:    "invalid template syntax",
			pattern: "{{.invalid",
			info: FileInfo{
				TemplateName: "test",
				ResourceName: "test",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GenerateFilename(tt.pattern, tt.info)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestWriteResults_SeparateFiles(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "claims-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	results := []RenderResult{
		{
			TemplateName: "template1",
			ResourceName: "resource1",
			Content:      "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: resource1",
		},
		{
			TemplateName: "template2",
			ResourceName: "resource2",
			Content:      "apiVersion: v1\nkind: Secret\nmetadata:\n  name: resource2",
		},
	}

	config := OutputConfig{
		Directory:       tmpDir,
		FilenamePattern: "{{.template}}-{{.name}}.yaml",
		SingleFile:      false,
		DryRun:          false,
	}

	err = WriteResults(results, config)
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	// Verify files were created
	file1 := filepath.Join(tmpDir, "template1-resource1.yaml")
	file2 := filepath.Join(tmpDir, "template2-resource2.yaml")

	content1, err := os.ReadFile(file1)
	if err != nil {
		t.Errorf("failed to read %s: %v", file1, err)
	}
	if string(content1) != results[0].Content {
		t.Errorf("file1 content mismatch")
	}

	content2, err := os.ReadFile(file2)
	if err != nil {
		t.Errorf("failed to read %s: %v", file2, err)
	}
	if string(content2) != results[1].Content {
		t.Errorf("file2 content mismatch")
	}
}

func TestWriteResults_SingleFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claims-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	results := []RenderResult{
		{
			TemplateName: "mytemplate",
			ResourceName: "resource1",
			Content:      "apiVersion: v1\nkind: ConfigMap",
		},
		{
			TemplateName: "mytemplate",
			ResourceName: "resource2",
			Content:      "apiVersion: v1\nkind: Secret",
		},
	}

	config := OutputConfig{
		Directory:  tmpDir,
		SingleFile: true,
		DryRun:     false,
	}

	err = WriteResults(results, config)
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	// Verify combined file was created
	combinedFile := filepath.Join(tmpDir, "mytemplate-combined.yaml")
	content, err := os.ReadFile(combinedFile)
	if err != nil {
		t.Fatalf("failed to read combined file: %v", err)
	}

	// Check that content is combined with ---
	if !strings.Contains(string(content), "---") {
		t.Errorf("combined file should contain --- separator")
	}
	if !strings.Contains(string(content), "ConfigMap") {
		t.Errorf("combined file should contain first resource")
	}
	if !strings.Contains(string(content), "Secret") {
		t.Errorf("combined file should contain second resource")
	}
}

func TestWriteResults_DryRun(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claims-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	results := []RenderResult{
		{
			TemplateName: "test",
			ResourceName: "test",
			Content:      "test content",
		},
	}

	config := OutputConfig{
		Directory:       tmpDir,
		FilenamePattern: "{{.template}}-{{.name}}.yaml",
		SingleFile:      false,
		DryRun:          true,
	}

	err = WriteResults(results, config)
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	// Verify no files were created
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("dry run should not create files, found %d", len(files))
	}
}

func TestWriteResults_CreatesDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claims-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Use a nested directory that doesn't exist
	nestedDir := filepath.Join(tmpDir, "nested", "output", "dir")

	results := []RenderResult{
		{
			TemplateName: "test",
			ResourceName: "test",
			Content:      "test content",
		},
	}

	config := OutputConfig{
		Directory:       nestedDir,
		FilenamePattern: "{{.template}}-{{.name}}.yaml",
		SingleFile:      false,
		DryRun:          false,
	}

	err = WriteResults(results, config)
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(nestedDir); os.IsNotExist(err) {
		t.Errorf("directory should have been created")
	}

	// Verify file exists
	file := filepath.Join(nestedDir, "test-test.yaml")
	if _, err := os.Stat(file); os.IsNotExist(err) {
		t.Errorf("file should have been created")
	}
}

func TestWriteResults_SkipsFailedResults(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claims-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	results := []RenderResult{
		{
			TemplateName: "success",
			ResourceName: "resource",
			Content:      "success content",
		},
		{
			TemplateName: "failed",
			ResourceName: "resource",
			Content:      "should not be written",
			Error:        &testError{},
		},
	}

	config := OutputConfig{
		Directory:       tmpDir,
		FilenamePattern: "{{.template}}-{{.name}}.yaml",
		SingleFile:      false,
		DryRun:          false,
	}

	err = WriteResults(results, config)
	if err != nil {
		t.Fatalf("WriteResults failed: %v", err)
	}

	// Verify only success file was created
	successFile := filepath.Join(tmpDir, "success-resource.yaml")
	failedFile := filepath.Join(tmpDir, "failed-resource.yaml")

	if _, err := os.Stat(successFile); os.IsNotExist(err) {
		t.Errorf("success file should exist")
	}
	if _, err := os.Stat(failedFile); !os.IsNotExist(err) {
		t.Errorf("failed file should not exist")
	}
}

func TestWriteSingleResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "claims-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := OutputConfig{
		Directory:       tmpDir,
		FilenamePattern: "{{.template}}-{{.name}}.yaml",
		SingleFile:      false,
		DryRun:          false,
	}

	err = WriteSingleResult("mytemplate", "myresource", "test: content", config)
	if err != nil {
		t.Fatalf("WriteSingleResult failed: %v", err)
	}

	file := filepath.Join(tmpDir, "mytemplate-myresource.yaml")
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != "test: content" {
		t.Errorf("content mismatch: got %q", string(content))
	}
}

// testError is a simple error type for testing
type testError struct{}

func (e *testError) Error() string { return "test error" }
