# Phase 4: Review Step

## Goal
Add a preview/review step showing rendered YAML before saving, with option to go back and edit parameters.

---

## Tasks

- [ ] Create `cmd/render_review.go`
- [ ] Add preview/review form showing rendered YAML
- [ ] Allow going back to edit parameters
- [ ] Add confirmation before proceeding

---

## Implementation

### `cmd/render_review.go`

```go
package cmd

import (
    "fmt"
    "strings"

    "github.com/charmbracelet/huh"
    "github.com/charmbracelet/lipgloss"
)

type ReviewAction string

const (
    ReviewActionContinue ReviewAction = "continue"
    ReviewActionEdit     ReviewAction = "edit"
    ReviewActionCancel   ReviewAction = "cancel"
)

var (
    reviewHeaderStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("205")).
        MarginTop(1).
        MarginBottom(1)

    resourceHeaderStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("86"))

    previewStyle = lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(lipgloss.Color("63")).
        Padding(0, 1).
        MaxHeight(15) // Limit height for long YAML
)

// ReviewResults displays rendered results and allows user to continue, edit, or cancel
func ReviewResults(results []RenderResult) (ReviewAction, int, error) {
    fmt.Println(reviewHeaderStyle.Render("━━━ Review Rendered Resources ━━━"))

    // Display each rendered resource
    for i, r := range results {
        if r.Error != nil {
            fmt.Printf("\n%s %s (ERROR: %v)\n",
                errorStyle.Render("✗"),
                r.TemplateName,
                r.Error,
            )
            continue
        }

        header := fmt.Sprintf("📄 %s (%s):", r.TemplateName, r.ResourceName)
        fmt.Println(resourceHeaderStyle.Render(header))

        // Truncate long YAML for preview
        preview := truncateYAML(r.Content, 20)
        fmt.Println(previewStyle.Render(preview))
        fmt.Println()
    }

    // Action selection
    var action string
    var editIndex int

    actionForm := huh.NewForm(
        huh.NewGroup(
            huh.NewSelect[string]().
                Title("What would you like to do?").
                Options(
                    huh.NewOption("Continue to save", "continue"),
                    huh.NewOption("Edit a template's parameters", "edit"),
                    huh.NewOption("Cancel", "cancel"),
                ).
                Value(&action),
        ),
    )

    if err := actionForm.Run(); err != nil {
        return ReviewActionCancel, 0, err
    }

    if action == "edit" {
        // Let user select which template to edit
        editIndex, err := selectTemplateToEdit(results)
        if err != nil {
            return ReviewActionCancel, 0, err
        }
        return ReviewActionEdit, editIndex, nil
    }

    return ReviewAction(action), 0, nil
}

func selectTemplateToEdit(results []RenderResult) (int, error) {
    options := make([]huh.Option[int], 0)
    for i, r := range results {
        if r.Error == nil {
            label := fmt.Sprintf("%s (%s)", r.TemplateName, r.ResourceName)
            options = append(options, huh.NewOption(label, i))
        }
    }

    var selected int
    form := huh.NewForm(
        huh.NewGroup(
            huh.NewSelect[int]().
                Title("Which template do you want to edit?").
                Options(options...).
                Value(&selected),
        ),
    )

    if err := form.Run(); err != nil {
        return 0, err
    }

    return selected, nil
}

func truncateYAML(content string, maxLines int) string {
    lines := strings.Split(content, "\n")
    if len(lines) <= maxLines {
        return content
    }

    truncated := strings.Join(lines[:maxLines], "\n")
    return truncated + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}

// ShowFullPreview displays complete YAML for a single result
func ShowFullPreview(result RenderResult) {
    fmt.Println(reviewHeaderStyle.Render(
        fmt.Sprintf("━━━ Full Preview: %s ━━━", result.TemplateName),
    ))
    fmt.Println(yamlStyle.Render(result.Content))
}
```

---

## Integration into Interactive Flow

```go
// cmd/render_interactive.go

func runInteractive(config *RenderConfig) error {
    // ... (steps 1-5: API, fetch, select, params, render)

    // 6. Review loop - allows going back to edit
    for {
        action, editIndex, err := ReviewResults(results)
        if err != nil {
            return err
        }

        switch action {
        case ReviewActionContinue:
            // Proceed to output configuration
            break

        case ReviewActionEdit:
            // Re-collect params for selected template
            tmpl := templateMap[results[editIndex].TemplateName]
            newParams, err := collectTemplateParams(tmpl)
            if err != nil {
                return err
            }

            // Re-render
            content, err := client.RenderTemplate(tmpl.Metadata.Name, newParams)
            if err != nil {
                results[editIndex].Error = err
            } else {
                results[editIndex].Content = content
                results[editIndex].Params = newParams
                if name, ok := newParams["name"]; ok {
                    results[editIndex].ResourceName = fmt.Sprintf("%v", name)
                }
            }
            continue // Loop back to review

        case ReviewActionCancel:
            fmt.Println("Cancelled.")
            return nil
        }

        break // Exit review loop
    }

    // 7. Output configuration
    // 8. Git operations
    // 9. Write files

    return nil
}
```

---

## Visual Design

```
━━━ Review Rendered Resources ━━━

📄 vsphere-vm (my-vm):
┌─────────────────────────────────────┐
│ apiVersion: infrastructure/v1       │
│ kind: VsphereVM                     │
│ metadata:                           │
│   name: my-vm                       │
│   namespace: infrastructure         │
│ spec:                               │
│   cpu: 4                            │
│   memory: 8Gi                       │
│ ... (12 more lines)                 │
└─────────────────────────────────────┘

📄 postgres-db (my-database):
┌─────────────────────────────────────┐
│ apiVersion: databases/v1            │
│ kind: PostgresDB                    │
│ ...                                 │
└─────────────────────────────────────┘

What would you like to do?
> ○ Continue to save
  ○ Edit a template's parameters
  ○ Cancel
```

---

## Verification

```bash
claims render
# After rendering, should show:
# 1. Preview of all rendered YAML
# 2. Option to continue, edit, or cancel
# 3. If edit: re-collect params, re-render, show preview again
```

---

## Files to Create

| File | Action |
|------|--------|
| `cmd/render_review.go` | Create |
| `cmd/render_interactive.go` | Add review loop |
