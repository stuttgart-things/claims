# Phase 3: Multi-Template Support

## Goal
Enable selecting and rendering multiple templates in a single operation.

---

## Tasks

- [ ] Implement `huh.MultiSelect` for template selection
- [ ] Add per-template parameter collection loop
- [ ] Add `--templates` flag for non-interactive use
- [ ] Render loop for multiple templates

---

## New Flag

```go
// cmd/render.go - add to init()
var templates []string
renderCmd.Flags().StringSliceVarP(&templates, "templates", "t", nil, "Templates to render (comma-separated or repeated)")
```

---

## Interactive Multi-Select

```go
// cmd/render_interactive.go

func selectTemplates(available []templates.ClaimTemplate) ([]string, error) {
    var selected []string

    // Build options from available templates
    options := make([]huh.Option[string], len(available))
    for i, t := range available {
        label := fmt.Sprintf("%s - %s", t.Metadata.Name, t.Metadata.Title)
        options[i] = huh.NewOption(label, t.Metadata.Name)
    }

    form := huh.NewForm(
        huh.NewGroup(
            huh.NewMultiSelect[string]().
                Title("Select templates to render").
                Description("Space to select, Enter to confirm").
                Options(options...).
                Value(&selected).
                Validate(func(s []string) error {
                    if len(s) == 0 {
                        return fmt.Errorf("select at least one template")
                    }
                    return nil
                }),
        ),
    )

    if err := form.Run(); err != nil {
        return nil, err
    }

    return selected, nil
}
```

---

## Per-Template Parameter Collection

```go
// cmd/render_interactive.go

type TemplateParams struct {
    TemplateName string
    Params       map[string]interface{}
}

func collectAllParams(client *templates.Client, selectedNames []string, templateMap map[string]*templates.ClaimTemplate) ([]TemplateParams, error) {
    var allParams []TemplateParams

    for i, name := range selectedNames {
        tmpl := templateMap[name]

        // Show progress header
        fmt.Printf("\n%s\n", titleStyle.Render(
            fmt.Sprintf("Configuring: %s (%d/%d)", tmpl.Metadata.Title, i+1, len(selectedNames)),
        ))
        fmt.Printf("%s\n\n", tmpl.Metadata.Description)

        // Collect params for this template (reuse existing createField logic)
        params, err := collectTemplateParams(tmpl)
        if err != nil {
            return nil, fmt.Errorf("collecting params for %s: %w", name, err)
        }

        allParams = append(allParams, TemplateParams{
            TemplateName: name,
            Params:       params,
        })
    }

    return allParams, nil
}

func collectTemplateParams(tmpl *templates.ClaimTemplate) (map[string]interface{}, error) {
    params := make(map[string]interface{})
    paramValues := make(map[string]*string)

    // Create form fields for each parameter
    var formGroups []*huh.Group
    var currentFields []huh.Field

    for _, p := range tmpl.Spec.Parameters {
        // Initialize with default
        defaultVal := ""
        if p.Default != nil {
            defaultVal = fmt.Sprintf("%v", p.Default)
        }
        paramValues[p.Name] = &defaultVal

        // Skip hidden parameters
        if p.Hidden {
            continue
        }

        field := createField(p, paramValues[p.Name])
        if field != nil {
            currentFields = append(currentFields, field)
        }

        // Group fields (max 5 per group)
        if len(currentFields) >= 5 {
            formGroups = append(formGroups, huh.NewGroup(currentFields...))
            currentFields = nil
        }
    }

    if len(currentFields) > 0 {
        formGroups = append(formGroups, huh.NewGroup(currentFields...))
    }

    if len(formGroups) > 0 {
        paramForm := huh.NewForm(formGroups...)
        if err := paramForm.Run(); err != nil {
            return nil, err
        }
    }

    // Resolve values
    for _, p := range tmpl.Spec.Parameters {
        strVal := *paramValues[p.Name]
        if strVal == "" {
            continue
        }
        // Handle random selection
        if strVal == randomMarker && len(p.Enum) > 0 {
            randomIdx := rand.Intn(len(p.Enum))
            strVal = p.Enum[randomIdx]
            fmt.Printf("Random selection for %s: %s\n", p.Name, strVal)
        }
        params[p.Name] = strVal
    }

    return params, nil
}
```

---

## Render Loop

```go
// cmd/render_interactive.go

func renderAllTemplates(client *templates.Client, allParams []TemplateParams) ([]RenderResult, error) {
    var results []RenderResult

    for _, tp := range allParams {
        fmt.Printf("Rendering %s...\n", tp.TemplateName)

        content, err := client.RenderTemplate(tp.TemplateName, tp.Params)
        if err != nil {
            results = append(results, RenderResult{
                TemplateName: tp.TemplateName,
                Error:        err,
            })
            continue
        }

        // Extract resource name for filename
        resourceName := "output"
        if name, ok := tp.Params["name"]; ok {
            resourceName = fmt.Sprintf("%v", name)
        }

        results = append(results, RenderResult{
            TemplateName: tp.TemplateName,
            ResourceName: resourceName,
            Content:      content,
            Params:       tp.Params,
        })
    }

    return results, nil
}
```

---

## Updated Interactive Flow

```go
// cmd/render_interactive.go

func runInteractive(config *RenderConfig) error {
    // 1. API URL confirmation (existing)
    // ...

    // 2. Fetch templates
    client := templates.NewClient(config.APIUrl)
    available, err := client.FetchTemplates()
    if err != nil {
        return err
    }

    // Build template map
    templateMap := make(map[string]*templates.ClaimTemplate)
    for i, t := range available {
        templateMap[t.Metadata.Name] = &available[i]
    }

    // 3. Multi-select templates
    selectedNames, err := selectTemplates(available)
    if err != nil {
        return err
    }

    // 4. Collect params for each selected template
    allParams, err := collectAllParams(client, selectedNames, templateMap)
    if err != nil {
        return err
    }

    // 5. Render all templates
    results, err := renderAllTemplates(client, allParams)
    if err != nil {
        return err
    }

    // 6. Review step (Phase 4)
    // 7. Output configuration (Phase 2)
    // 8. Git operations (Phase 6)
    // 9. Write files

    return nil
}
```

---

## Backward Compatibility

When only one template is selected, the flow should feel identical to the current single-template experience:
- Same parameter forms
- Same confirmation
- Same output

---

## Verification

```bash
# Interactive multi-select
claims render
# Shows MultiSelect, allows choosing multiple templates

# Non-interactive with multiple templates
claims render --non-interactive -t vsphere-vm -t postgres-db -f params.yaml

# Non-interactive comma-separated
claims render --non-interactive -t vsphere-vm,postgres-db -f params.yaml
```

---

## Files to Modify

| File | Action |
|------|--------|
| `cmd/render.go` | Add `--templates` flag |
| `cmd/render_interactive.go` | Add multi-select, param collection loop |
