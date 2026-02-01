# Phase 5: Non-Interactive Mode

## Goal
Enable fully automated rendering via flags and parameter files for CI/CD pipelines.

---

## Tasks

- [ ] Create `internal/params/file.go` (YAML/JSON parsing)
- [ ] Create `cmd/render_noninteractive.go`
- [ ] Add `--params-file`, `--param`, `--interactive`, `--non-interactive` flags
- [ ] TTY detection for auto mode selection

---

## New Flags

```go
// cmd/render.go - add to init()
var (
    paramsFile     string
    inlineParams   []string
    interactive    bool
    nonInteractive bool
)

renderCmd.Flags().StringVarP(&paramsFile, "params-file", "f", "", "YAML/JSON file with parameters")
renderCmd.Flags().StringSliceVarP(&inlineParams, "param", "p", nil, "Inline param (key=value, repeatable)")
renderCmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Force interactive mode")
renderCmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Force non-interactive mode")
```

---

## Parameter File Format

### `internal/params/types.go`

```go
package params

// ParameterFile supports both single and multi-template formats
type ParameterFile struct {
    // Single template format
    Template   string                 `yaml:"template" json:"template"`
    Parameters map[string]interface{} `yaml:"parameters" json:"parameters"`

    // Multi-template format
    Templates []TemplateParams `yaml:"templates" json:"templates"`
}

type TemplateParams struct {
    Name       string                 `yaml:"name" json:"name"`
    Parameters map[string]interface{} `yaml:"parameters" json:"parameters"`
}

// Normalize converts single-template format to multi-template
func (pf *ParameterFile) Normalize() {
    if pf.Template != "" && len(pf.Templates) == 0 {
        pf.Templates = []TemplateParams{{
            Name:       pf.Template,
            Parameters: pf.Parameters,
        }}
    }
}
```

### `internal/params/file.go`

```go
package params

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "gopkg.in/yaml.v3"
)

// ParseFile reads and parses a parameter file (YAML or JSON)
func ParseFile(path string) (*ParameterFile, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("reading params file: %w", err)
    }

    var pf ParameterFile

    // Detect format by extension or try both
    ext := strings.ToLower(filepath.Ext(path))

    switch ext {
    case ".json":
        if err := json.Unmarshal(data, &pf); err != nil {
            return nil, fmt.Errorf("parsing JSON: %w", err)
        }
    case ".yaml", ".yml":
        if err := yaml.Unmarshal(data, &pf); err != nil {
            return nil, fmt.Errorf("parsing YAML: %w", err)
        }
    default:
        // Try YAML first, then JSON
        if err := yaml.Unmarshal(data, &pf); err != nil {
            if err := json.Unmarshal(data, &pf); err != nil {
                return nil, fmt.Errorf("parsing params file (tried YAML and JSON): %w", err)
            }
        }
    }

    pf.Normalize()
    return &pf, nil
}

// ParseInlineParams parses key=value strings into a map
func ParseInlineParams(params []string) (map[string]interface{}, error) {
    result := make(map[string]interface{})

    for _, p := range params {
        parts := strings.SplitN(p, "=", 2)
        if len(parts) != 2 {
            return nil, fmt.Errorf("invalid param format: %s (expected key=value)", p)
        }
        result[parts[0]] = parts[1]
    }

    return result, nil
}

// MergeParams merges file params with inline params (inline takes precedence)
func MergeParams(fileParams, inlineParams map[string]interface{}) map[string]interface{} {
    result := make(map[string]interface{})

    for k, v := range fileParams {
        result[k] = v
    }
    for k, v := range inlineParams {
        result[k] = v
    }

    return result
}
```

---

## Non-Interactive Execution

### `cmd/render_noninteractive.go`

```go
package cmd

import (
    "fmt"
    "os"

    "github.com/stuttgart-things/claims/internal/params"
    "github.com/stuttgart-things/claims/internal/templates"
)

func runNonInteractive(config *RenderConfig) error {
    // Validate required inputs
    if config.ParamsFile == "" && len(config.Templates) == 0 {
        return fmt.Errorf("non-interactive mode requires --params-file or --templates")
    }

    client := templates.NewClient(config.APIUrl)

    // Parse parameter file if provided
    var templateParams []params.TemplateParams
    if config.ParamsFile != "" {
        pf, err := params.ParseFile(config.ParamsFile)
        if err != nil {
            return err
        }
        templateParams = pf.Templates
    }

    // If templates specified via flag, use those
    if len(config.Templates) > 0 {
        // Parse inline params
        inlineParams, err := params.ParseInlineParams(config.InlineParams)
        if err != nil {
            return err
        }

        // Override or create template params
        for _, tmplName := range config.Templates {
            // Find existing params from file or create new
            found := false
            for i, tp := range templateParams {
                if tp.Name == tmplName {
                    templateParams[i].Parameters = params.MergeParams(tp.Parameters, inlineParams)
                    found = true
                    break
                }
            }
            if !found {
                templateParams = append(templateParams, params.TemplateParams{
                    Name:       tmplName,
                    Parameters: inlineParams,
                })
            }
        }
    }

    // Validate templates exist
    available, err := client.FetchTemplates()
    if err != nil {
        return fmt.Errorf("fetching templates: %w", err)
    }
    templateMap := make(map[string]bool)
    for _, t := range available {
        templateMap[t.Metadata.Name] = true
    }
    for _, tp := range templateParams {
        if !templateMap[tp.Name] {
            return fmt.Errorf("template not found: %s", tp.Name)
        }
    }

    // Render all templates
    var results []RenderResult
    for _, tp := range templateParams {
        fmt.Printf("Rendering %s...\n", tp.Name)

        content, err := client.RenderTemplate(tp.Name, tp.Parameters)
        if err != nil {
            fmt.Printf("  ERROR: %v\n", err)
            results = append(results, RenderResult{
                TemplateName: tp.Name,
                Error:        err,
            })
            continue
        }

        resourceName := "output"
        if name, ok := tp.Parameters["name"]; ok {
            resourceName = fmt.Sprintf("%v", name)
        }

        results = append(results, RenderResult{
            TemplateName: tp.Name,
            ResourceName: resourceName,
            Content:      content,
            Params:       tp.Parameters,
        })
        fmt.Printf("  ✓ Rendered successfully\n")
    }

    // Check for any errors
    hasErrors := false
    for _, r := range results {
        if r.Error != nil {
            hasErrors = true
        }
    }

    // Write output
    outputConfig := OutputConfig{
        Directory:       config.OutputDir,
        FilenamePattern: config.FilenamePattern,
        SingleFile:      config.SingleFile,
        DryRun:          config.DryRun,
    }

    if err := WriteResults(results, outputConfig); err != nil {
        return err
    }

    // Git operations (Phase 6)
    // PR creation (Phase 7)

    if hasErrors {
        return fmt.Errorf("some templates failed to render")
    }

    return nil
}
```

---

## Mode Detection

```go
// cmd/render.go

import (
    "os"
    "golang.org/x/term"
)

func runRender(cmd *cobra.Command, args []string) {
    config := buildRenderConfig()

    // Determine mode
    if nonInteractive {
        config.Interactive = false
    } else if interactive {
        config.Interactive = true
    } else {
        // Auto-detect: interactive if TTY, non-interactive otherwise
        config.Interactive = term.IsTerminal(int(os.Stdin.Fd()))
    }

    var err error
    if config.Interactive {
        err = runInteractive(config)
    } else {
        err = runNonInteractive(config)
    }

    if err != nil {
        fmt.Println(errorStyle.Render(err.Error()))
        os.Exit(1)
    }
}
```

---

## Example Parameter Files

### Single Template (`params-single.yaml`)
```yaml
template: vsphere-vm
parameters:
  name: my-vm
  namespace: infrastructure
  cpu: 4
  memory: 8Gi
```

### Multi-Template (`params-multi.yaml`)
```yaml
templates:
  - name: vsphere-vm
    parameters:
      name: my-vm
      namespace: infrastructure
      cpu: 4

  - name: postgres-db
    parameters:
      name: my-database
      namespace: databases
      version: "15"
```

---

## Verification

```bash
# With params file
claims render --non-interactive -f params.yaml -o ./out

# With inline params
claims render --non-interactive -t vsphere-vm -p name=my-vm -p cpu=4 -o ./out

# Mixed: file + inline override
claims render --non-interactive -f params.yaml -p cpu=8 -o ./out

# Auto-detect mode (non-interactive in CI)
echo "" | claims render -f params.yaml -o ./out
```

---

## Dependencies to Add

```go
// go.mod
require (
    gopkg.in/yaml.v3 v3.0.1
    golang.org/x/term v0.x.x
)
```

---

## Files to Create

| File | Action |
|------|--------|
| `internal/params/types.go` | Create |
| `internal/params/file.go` | Create |
| `cmd/render_noninteractive.go` | Create |
| `cmd/render.go` | Add flags, mode detection |
