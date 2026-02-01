# Phase 8: Polish & Documentation

## Goal
Production readiness with tests, error handling, and documentation.

---

## Tasks

- [ ] Add unit tests for each package
- [ ] Add integration tests
- [ ] Update go.mod with new dependencies
- [ ] Move plan to `docs/IMPLEMENTATION_PLAN.md`
- [ ] Create GitHub issues from phases
- [ ] Update README with new features

---

## Unit Tests

### `internal/templates/client_test.go`

```go
package templates_test

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stuttgart-things/claims/internal/templates"
)

func TestFetchTemplates(t *testing.T) {
    // Mock server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{
            "apiVersion": "v1",
            "kind": "ClaimTemplateList",
            "items": [
                {
                    "metadata": {"name": "test-template", "title": "Test"},
                    "spec": {"parameters": []}
                }
            ]
        }`))
    }))
    defer server.Close()

    client := templates.NewClient(server.URL)
    templates, err := client.FetchTemplates()

    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(templates) != 1 {
        t.Fatalf("expected 1 template, got %d", len(templates))
    }
    if templates[0].Metadata.Name != "test-template" {
        t.Errorf("expected name 'test-template', got '%s'", templates[0].Metadata.Name)
    }
}
```

### `internal/params/file_test.go`

```go
package params_test

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/stuttgart-things/claims/internal/params"
)

func TestParseSingleTemplateYAML(t *testing.T) {
    content := `
template: test-vm
parameters:
  name: my-vm
  cpu: 4
`
    tmpFile := writeTempFile(t, "params.yaml", content)
    defer os.Remove(tmpFile)

    pf, err := params.ParseFile(tmpFile)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if len(pf.Templates) != 1 {
        t.Fatalf("expected 1 template, got %d", len(pf.Templates))
    }
    if pf.Templates[0].Name != "test-vm" {
        t.Errorf("expected name 'test-vm', got '%s'", pf.Templates[0].Name)
    }
}

func TestParseMultiTemplateYAML(t *testing.T) {
    content := `
templates:
  - name: vm-1
    parameters:
      name: first
  - name: vm-2
    parameters:
      name: second
`
    tmpFile := writeTempFile(t, "params.yaml", content)
    defer os.Remove(tmpFile)

    pf, err := params.ParseFile(tmpFile)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if len(pf.Templates) != 2 {
        t.Fatalf("expected 2 templates, got %d", len(pf.Templates))
    }
}

func TestParseInlineParams(t *testing.T) {
    params := []string{"name=test", "cpu=4", "memory=8Gi"}

    result, err := params.ParseInlineParams(params)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }

    if result["name"] != "test" {
        t.Errorf("expected name 'test', got '%v'", result["name"])
    }
}

func writeTempFile(t *testing.T, name, content string) string {
    t.Helper()
    tmpDir := t.TempDir()
    path := filepath.Join(tmpDir, name)
    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        t.Fatal(err)
    }
    return path
}
```

### `cmd/render_output_test.go`

```go
package cmd_test

import (
    "testing"

    "github.com/stuttgart-things/claims/cmd"
)

func TestGenerateFilename(t *testing.T) {
    tests := []struct {
        pattern  string
        info     cmd.FileInfo
        expected string
    }{
        {
            pattern:  "{{.template}}-{{.name}}.yaml",
            info:     cmd.FileInfo{TemplateName: "vsphere-vm", ResourceName: "my-vm"},
            expected: "vsphere-vm-my-vm.yaml",
        },
        {
            pattern:  "{{.name}}.yaml",
            info:     cmd.FileInfo{TemplateName: "vsphere-vm", ResourceName: "my-vm"},
            expected: "my-vm.yaml",
        },
    }

    for _, tt := range tests {
        result, err := cmd.GenerateFilename(tt.pattern, tt.info)
        if err != nil {
            t.Errorf("unexpected error: %v", err)
        }
        if result != tt.expected {
            t.Errorf("expected '%s', got '%s'", tt.expected, result)
        }
    }
}
```

---

## Integration Tests

### `tests/integration/render_test.go`

```go
//go:build integration

package integration

import (
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestRenderNonInteractive(t *testing.T) {
    if os.Getenv("CLAIM_API_URL") == "" {
        t.Skip("CLAIM_API_URL not set")
    }

    tmpDir := t.TempDir()
    paramsFile := filepath.Join(tmpDir, "params.yaml")

    // Write test params file
    params := `
template: test-template
parameters:
  name: integration-test
`
    os.WriteFile(paramsFile, []byte(params), 0644)

    // Run claims render
    cmd := exec.Command("claims", "render",
        "--non-interactive",
        "-f", paramsFile,
        "-o", tmpDir,
    )
    cmd.Env = os.Environ()

    output, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("render failed: %v\n%s", err, output)
    }

    // Check output file exists
    expectedFile := filepath.Join(tmpDir, "test-template-integration-test.yaml")
    if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
        t.Errorf("expected output file not found: %s", expectedFile)
    }
}
```

---

## Updated go.mod

```go
module github.com/stuttgart-things/claims

go 1.25.5

require (
    github.com/charmbracelet/huh v0.8.0
    github.com/charmbracelet/lipgloss v1.1.0
    github.com/go-git/go-git/v5 v5.12.0
    github.com/lucasb-eyer/go-colorful v1.3.0
    github.com/spf13/cobra v1.10.2
    golang.org/x/term v0.27.0
    gopkg.in/yaml.v3 v3.0.1
)
```

---

## Updated README.md

Add section for new features:

```markdown
## Multi-Template Rendering

Render multiple templates in a single operation:

### Interactive Mode
```bash
claims render
# Select multiple templates with space bar
# Fill parameters for each
# Review rendered YAML
# Choose output options
# Optionally commit and push
```

### Non-Interactive Mode
```bash
# Single template
claims render --non-interactive -t vsphere-vm -p name=my-vm -o ./manifests

# Multiple templates with params file
claims render --non-interactive -f params.yaml -o ./manifests

# With GitOps
claims render --non-interactive -f params.yaml -o ./manifests \
  --git-push --create-pr --pr-labels "infrastructure"
```

### Parameter File Format

```yaml
templates:
  - name: vsphere-vm
    parameters:
      name: my-vm
      cpu: 4
  - name: postgres-db
    parameters:
      name: my-db
      version: "15"
```

## Flags Reference

| Flag | Short | Description |
|------|-------|-------------|
| `--api-url` | `-a` | API URL |
| `--templates` | `-t` | Templates to render |
| `--params-file` | `-f` | Parameter file (YAML/JSON) |
| `--param` | `-p` | Inline parameter |
| `--output-dir` | `-o` | Output directory |
| `--single-file` | | Combine into one file |
| `--git-commit` | `-g` | Commit files |
| `--git-push` | | Push to remote |
| `--create-pr` | | Create pull request |
| `--pr-labels` | | PR labels |
```

---

## GitHub Issues

Create issues for each phase:

1. **Issue: Foundation Refactoring**
   - Labels: `enhancement`, `refactoring`
   - Milestone: v0.2.0

2. **Issue: Output Control**
   - Labels: `enhancement`, `feature`
   - Depends on: #1

3. **Issue: Multi-Template Support**
   - Labels: `enhancement`, `feature`
   - Depends on: #1

4. **Issue: Review Step**
   - Labels: `enhancement`, `ux`
   - Depends on: #3

5. **Issue: Non-Interactive Mode**
   - Labels: `enhancement`, `feature`
   - Depends on: #2, #3

6. **Issue: GitOps Integration**
   - Labels: `enhancement`, `feature`
   - Depends on: #2

7. **Issue: Pull Request Support**
   - Labels: `enhancement`, `feature`
   - Depends on: #6

8. **Issue: Testing & Documentation**
   - Labels: `testing`, `documentation`
   - Depends on: all

---

## Files to Create/Modify

| File | Action |
|------|--------|
| `internal/templates/client_test.go` | Create |
| `internal/params/file_test.go` | Create |
| `cmd/render_output_test.go` | Create |
| `tests/integration/render_test.go` | Create |
| `go.mod` | Update dependencies |
| `README.md` | Update documentation |
| `docs/IMPLEMENTATION_PLAN.md` | Move plan here |

---

## Verification Checklist

- [ ] All unit tests pass: `go test ./...`
- [ ] Integration tests pass: `go test -tags=integration ./tests/...`
- [ ] Linting passes: `golangci-lint run`
- [ ] Build succeeds: `go build`
- [ ] Interactive mode works end-to-end
- [ ] Non-interactive mode works with params file
- [ ] Git commit/push works
- [ ] PR creation works
- [ ] Backward compatibility maintained
