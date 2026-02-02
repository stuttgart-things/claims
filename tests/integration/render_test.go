//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderNonInteractive tests the non-interactive render workflow
func TestRenderNonInteractive(t *testing.T) {
	apiURL := os.Getenv("CLAIM_API_URL")
	if apiURL == "" {
		t.Skip("CLAIM_API_URL not set, skipping integration test")
	}

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "claims-test", ".")
	buildCmd.Dir = getProjectRoot(t)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build: %v\n%s", err, output)
	}
	defer os.Remove(filepath.Join(getProjectRoot(t), "claims-test"))

	tmpDir := t.TempDir()
	paramsFile := filepath.Join(tmpDir, "params.yaml")

	// Write test params file
	params := `template: vspherevm
parameters:
  name: integration-test-vm
  cpu: 2
  memory: 4Gi
`
	if err := os.WriteFile(paramsFile, []byte(params), 0644); err != nil {
		t.Fatalf("failed to write params file: %v", err)
	}

	// Run claims render in non-interactive mode
	cmd := exec.Command(
		filepath.Join(getProjectRoot(t), "claims-test"),
		"render",
		"--non-interactive",
		"-f", paramsFile,
		"-o", tmpDir,
		"-a", apiURL,
	)
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render failed: %v\n%s", err, output)
	}

	// Check that output mentions rendering
	if !strings.Contains(string(output), "Rendering") {
		t.Errorf("expected output to contain 'Rendering', got: %s", output)
	}
}

// TestRenderWithInlineParams tests rendering with inline parameters
func TestRenderWithInlineParams(t *testing.T) {
	apiURL := os.Getenv("CLAIM_API_URL")
	if apiURL == "" {
		t.Skip("CLAIM_API_URL not set, skipping integration test")
	}

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "claims-test", ".")
	buildCmd.Dir = getProjectRoot(t)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build: %v\n%s", err, output)
	}
	defer os.Remove(filepath.Join(getProjectRoot(t), "claims-test"))

	tmpDir := t.TempDir()

	// Run claims render with inline params
	cmd := exec.Command(
		filepath.Join(getProjectRoot(t), "claims-test"),
		"render",
		"--non-interactive",
		"-t", "vspherevm",
		"-p", "name=inline-test",
		"-p", "cpu=4",
		"-o", tmpDir,
		"-a", apiURL,
	)
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render failed: %v\n%s", err, output)
	}

	// Check that output mentions rendering
	if !strings.Contains(string(output), "Rendering") {
		t.Errorf("expected output to contain 'Rendering', got: %s", output)
	}
}

// TestRenderDryRun tests that dry-run mode doesn't write files
func TestRenderDryRun(t *testing.T) {
	apiURL := os.Getenv("CLAIM_API_URL")
	if apiURL == "" {
		t.Skip("CLAIM_API_URL not set, skipping integration test")
	}

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "claims-test", ".")
	buildCmd.Dir = getProjectRoot(t)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build: %v\n%s", err, output)
	}
	defer os.Remove(filepath.Join(getProjectRoot(t), "claims-test"))

	tmpDir := t.TempDir()
	paramsFile := filepath.Join(tmpDir, "params.yaml")

	// Write test params file
	params := `template: vspherevm
parameters:
  name: dry-run-test
`
	if err := os.WriteFile(paramsFile, []byte(params), 0644); err != nil {
		t.Fatalf("failed to write params file: %v", err)
	}

	outputDir := filepath.Join(tmpDir, "output")

	// Run claims render with dry-run
	cmd := exec.Command(
		filepath.Join(getProjectRoot(t), "claims-test"),
		"render",
		"--non-interactive",
		"-f", paramsFile,
		"-o", outputDir,
		"--dry-run",
		"-a", apiURL,
	)
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render failed: %v\n%s", err, output)
	}

	// Check that output mentions DRY RUN
	if !strings.Contains(string(output), "DRY RUN") {
		t.Errorf("expected output to contain 'DRY RUN', got: %s", output)
	}

	// Verify no files were written (directory shouldn't exist or should be empty)
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		files, _ := os.ReadDir(outputDir)
		if len(files) > 0 {
			t.Errorf("dry-run should not create files, found: %v", files)
		}
	}
}

// TestRenderMultipleTemplates tests rendering multiple templates from a params file
func TestRenderMultipleTemplates(t *testing.T) {
	apiURL := os.Getenv("CLAIM_API_URL")
	if apiURL == "" {
		t.Skip("CLAIM_API_URL not set, skipping integration test")
	}

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "claims-test", ".")
	buildCmd.Dir = getProjectRoot(t)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build: %v\n%s", err, output)
	}
	defer os.Remove(filepath.Join(getProjectRoot(t), "claims-test"))

	tmpDir := t.TempDir()
	paramsFile := filepath.Join(tmpDir, "params.yaml")

	// Write multi-template params file
	params := `templates:
  - name: vspherevm
    parameters:
      name: multi-test-vm
      cpu: 2
  - name: postgresql
    parameters:
      name: multi-test-db
      version: "15"
`
	if err := os.WriteFile(paramsFile, []byte(params), 0644); err != nil {
		t.Fatalf("failed to write params file: %v", err)
	}

	// Run claims render
	cmd := exec.Command(
		filepath.Join(getProjectRoot(t), "claims-test"),
		"render",
		"--non-interactive",
		"-f", paramsFile,
		"-o", tmpDir,
		"-a", apiURL,
	)
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render failed: %v\n%s", err, output)
	}

	// Check for both templates being rendered
	outputStr := string(output)
	if !strings.Contains(outputStr, "vspherevm") {
		t.Errorf("expected output to mention vspherevm")
	}
	if !strings.Contains(outputStr, "postgresql") {
		t.Errorf("expected output to mention postgresql")
	}
}

// TestRenderSingleFile tests combining output into a single file
func TestRenderSingleFile(t *testing.T) {
	apiURL := os.Getenv("CLAIM_API_URL")
	if apiURL == "" {
		t.Skip("CLAIM_API_URL not set, skipping integration test")
	}

	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "claims-test", ".")
	buildCmd.Dir = getProjectRoot(t)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build: %v\n%s", err, output)
	}
	defer os.Remove(filepath.Join(getProjectRoot(t), "claims-test"))

	tmpDir := t.TempDir()
	paramsFile := filepath.Join(tmpDir, "params.yaml")

	// Write multi-template params file
	params := `templates:
  - name: vspherevm
    parameters:
      name: single-file-vm
  - name: postgresql
    parameters:
      name: single-file-db
`
	if err := os.WriteFile(paramsFile, []byte(params), 0644); err != nil {
		t.Fatalf("failed to write params file: %v", err)
	}

	// Run claims render with --single-file
	cmd := exec.Command(
		filepath.Join(getProjectRoot(t), "claims-test"),
		"render",
		"--non-interactive",
		"-f", paramsFile,
		"-o", tmpDir,
		"--single-file",
		"-a", apiURL,
	)
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render failed: %v\n%s", err, output)
	}

	// Check that combined.yaml was created
	combinedFile := filepath.Join(tmpDir, "combined.yaml")
	if _, err := os.Stat(combinedFile); os.IsNotExist(err) {
		t.Errorf("expected combined.yaml to be created")
	}
}

// TestVersionCommand tests the version command
func TestVersionCommand(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "claims-test", ".")
	buildCmd.Dir = getProjectRoot(t)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build: %v\n%s", err, output)
	}
	defer os.Remove(filepath.Join(getProjectRoot(t), "claims-test"))

	cmd := exec.Command(filepath.Join(getProjectRoot(t), "claims-test"), "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version command failed: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "claims") {
		t.Errorf("expected version output to contain 'claims', got: %s", output)
	}
}

// TestHelpCommand tests the help command
func TestHelpCommand(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "claims-test", ".")
	buildCmd.Dir = getProjectRoot(t)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build: %v\n%s", err, output)
	}
	defer os.Remove(filepath.Join(getProjectRoot(t), "claims-test"))

	cmd := exec.Command(filepath.Join(getProjectRoot(t), "claims-test"), "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("help command failed: %v\n%s", err, output)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "render") {
		t.Errorf("expected help to mention render command")
	}
	if !strings.Contains(outputStr, "version") {
		t.Errorf("expected help to mention version command")
	}
}

// TestRenderHelpCommand tests the render help command
func TestRenderHelpCommand(t *testing.T) {
	// Build the binary first
	buildCmd := exec.Command("go", "build", "-o", "claims-test", ".")
	buildCmd.Dir = getProjectRoot(t)
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build: %v\n%s", err, output)
	}
	defer os.Remove(filepath.Join(getProjectRoot(t), "claims-test"))

	cmd := exec.Command(filepath.Join(getProjectRoot(t), "claims-test"), "render", "--help")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render help command failed: %v\n%s", err, output)
	}

	outputStr := string(output)
	// Check for expected flags
	expectedFlags := []string{
		"--api-url",
		"--non-interactive",
		"--params-file",
		"--output-dir",
		"--dry-run",
	}

	for _, flag := range expectedFlags {
		if !strings.Contains(outputStr, flag) {
			t.Errorf("expected render help to mention %s", flag)
		}
	}
}

// getProjectRoot returns the project root directory
func getProjectRoot(t *testing.T) string {
	t.Helper()

	// Get the directory of this test file
	_, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Navigate up to project root (from tests/integration)
	projectRoot := filepath.Join("..", "..")

	// Verify it's the right directory by checking for go.mod
	if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); os.IsNotExist(err) {
		// Try absolute path
		projectRoot = "/home/sthings/projects/claims"
	}

	return projectRoot
}
