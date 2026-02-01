# Phase 2: Output Control

## Goal
Add flexible output options: custom directory, single/multiple files, filename patterns.

---

## Tasks

- [ ] Create `cmd/render_output.go`
- [ ] Add `--output-dir`, `--dry-run`, `--single-file`, `--filename-pattern` flags
- [ ] Implement single-file vs separate-files output modes
- [ ] Update interactive flow with output organization form

---

## New Flags

```go
// cmd/render.go - add to init()
renderCmd.Flags().StringVarP(&outputDir, "output-dir", "o", "/tmp", "Output directory for rendered files")
renderCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print output without writing files")
renderCmd.Flags().BoolVar(&singleFile, "single-file", false, "Combine all resources into one file")
renderCmd.Flags().StringVar(&filenamePattern, "filename-pattern", "{{.template}}-{{.name}}.yaml", "Pattern for output filenames")
```

---

## Implementation

### `cmd/render_output.go`

```go
package cmd

import (
    "bytes"
    "fmt"
    "os"
    "path/filepath"
    "strings"
    "text/template"
)

type OutputConfig struct {
    Directory       string
    FilenamePattern string
    SingleFile      bool
    DryRun          bool
}

type FileInfo struct {
    TemplateName string
    ResourceName string
}

// GenerateFilename creates filename from pattern and file info
func GenerateFilename(pattern string, info FileInfo) (string, error) {
    tmpl, err := template.New("filename").Parse(pattern)
    if err != nil {
        return "", fmt.Errorf("invalid filename pattern: %w", err)
    }

    data := map[string]string{
        "template": info.TemplateName,
        "name":     info.ResourceName,
    }

    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", fmt.Errorf("executing filename template: %w", err)
    }

    return buf.String(), nil
}

// WriteResults writes render results to files
func WriteResults(results []RenderResult, config OutputConfig) error {
    if config.DryRun {
        return printDryRun(results, config)
    }

    // Ensure output directory exists
    if err := os.MkdirAll(config.Directory, 0755); err != nil {
        return fmt.Errorf("creating output directory: %w", err)
    }

    if config.SingleFile {
        return writeSingleFile(results, config)
    }
    return writeSeparateFiles(results, config)
}

func writeSingleFile(results []RenderResult, config OutputConfig) error {
    var combined strings.Builder

    for i, r := range results {
        if i > 0 {
            combined.WriteString("\n---\n")
        }
        combined.WriteString(r.Content)
    }

    // Use first template name for combined file
    filename := "combined-claims.yaml"
    if len(results) > 0 {
        filename = fmt.Sprintf("%s-combined.yaml", results[0].TemplateName)
    }

    path := filepath.Join(config.Directory, filename)
    if err := os.WriteFile(path, []byte(combined.String()), 0644); err != nil {
        return fmt.Errorf("writing combined file: %w", err)
    }

    fmt.Printf("Saved combined file: %s\n", path)
    return nil
}

func writeSeparateFiles(results []RenderResult, config OutputConfig) error {
    for _, r := range results {
        filename, err := GenerateFilename(config.FilenamePattern, FileInfo{
            TemplateName: r.TemplateName,
            ResourceName: r.ResourceName,
        })
        if err != nil {
            return err
        }

        path := filepath.Join(config.Directory, filename)
        if err := os.WriteFile(path, []byte(r.Content), 0644); err != nil {
            return fmt.Errorf("writing %s: %w", path, err)
        }

        r.OutputPath = path
        fmt.Printf("Saved: %s\n", path)
    }
    return nil
}

func printDryRun(results []RenderResult, config OutputConfig) error {
    fmt.Println("\n=== DRY RUN - No files written ===\n")

    for _, r := range results {
        filename, _ := GenerateFilename(config.FilenamePattern, FileInfo{
            TemplateName: r.TemplateName,
            ResourceName: r.ResourceName,
        })
        path := filepath.Join(config.Directory, filename)

        fmt.Printf("Would write: %s\n", path)
        fmt.Println(yamlStyle.Render(r.Content))
        fmt.Println()
    }
    return nil
}
```

---

## Interactive Form

```go
// cmd/render_interactive.go - add output organization form

func runOutputForm() (*OutputConfig, error) {
    var (
        outputMode      string // "separate" or "single"
        outputDir       string = "./manifests"
        filenamePattern string = "{{.template}}-{{.name}}.yaml"
    )

    form := huh.NewForm(
        huh.NewGroup(
            huh.NewSelect[string]().
                Title("How should resources be saved?").
                Options(
                    huh.NewOption("Separate files (one per resource)", "separate"),
                    huh.NewOption("Single file (combined with ---)", "single"),
                ).
                Value(&outputMode),

            huh.NewInput().
                Title("Output directory").
                Description("Where to save rendered files").
                Value(&outputDir),
        ),
    )

    // Only show filename pattern for separate files
    if outputMode == "separate" {
        patternForm := huh.NewForm(
            huh.NewGroup(
                huh.NewSelect[string]().
                    Title("Filename pattern").
                    Options(
                        huh.NewOption("{{template}}-{{name}}.yaml", "{{.template}}-{{.name}}.yaml"),
                        huh.NewOption("{{name}}.yaml", "{{.name}}.yaml"),
                        huh.NewOption("Custom", "custom"),
                    ).
                    Value(&filenamePattern),
            ),
        )
        if err := patternForm.Run(); err != nil {
            return nil, err
        }
    }

    if err := form.Run(); err != nil {
        return nil, err
    }

    return &OutputConfig{
        Directory:       outputDir,
        FilenamePattern: filenamePattern,
        SingleFile:      outputMode == "single",
    }, nil
}
```

---

## Verification

```bash
# Separate files (default)
claims render -o ./out
# Creates: ./out/template-name.yaml for each

# Single combined file
claims render -o ./out --single-file
# Creates: ./out/combined-claims.yaml

# Custom pattern
claims render -o ./out --filename-pattern "{{.name}}.yaml"
# Creates: ./out/my-resource.yaml

# Dry run
claims render --dry-run
# Prints what would be written without creating files
```

---

## Files to Create/Modify

| File | Action |
|------|--------|
| `cmd/render_output.go` | Create |
| `cmd/render.go` | Add flags |
| `cmd/render_interactive.go` | Add output form |
