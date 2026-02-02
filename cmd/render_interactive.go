package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/stuttgart-things/claims/internal/templates"
)

const randomMarker = "🎲 Random"

// Styles for terminal output
var (
	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("42"))

	yamlStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	progressStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)
)

// TemplateParams holds parameters for a single template
type TemplateParams struct {
	TemplateName string
	Params       map[string]any
}

// runInteractiveRender runs the interactive render flow
func runInteractiveRender(client *templates.Client) {
	// Fetch templates from API
	templateList, err := client.FetchTemplates()
	if err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Failed to fetch templates: %v", err)))
		os.Exit(1)
	}

	fmt.Printf("Loaded %d templates from API\n\n", len(templateList))

	// Build template map
	templateMap := make(map[string]*templates.ClaimTemplate)
	for i, t := range templateList {
		templateMap[t.Metadata.Name] = &templateList[i]
	}

	// Select templates (multi-select or use flag values)
	var selectedNames []string
	if len(templateNames) > 0 {
		// Validate provided template names
		for _, name := range templateNames {
			if _, exists := templateMap[name]; !exists {
				fmt.Println(errorStyle.Render(fmt.Sprintf("Template not found: %s", name)))
				os.Exit(1)
			}
		}
		selectedNames = templateNames
	} else {
		// Interactive multi-select
		selectedNames, err = selectTemplates(templateList)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("\nSelected %d template(s): %v\n", len(selectedNames), selectedNames)

	// Collect parameters for each selected template
	allParams, err := collectAllParams(selectedNames, templateMap)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Confirm before rendering
	confirm := true
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("Render %d template(s)?", len(selectedNames))).
				Description("This will call the API to generate YAML").
				Affirmative("Yes, render").
				Negative("Cancel").
				Value(&confirm),
		),
	)

	if err := confirmForm.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if !confirm {
		fmt.Println("Cancelled.")
		os.Exit(0)
	}

	// Render all templates
	fmt.Println("\nRendering templates...")
	results := renderAllTemplates(client, allParams)

	// Review loop - allows going back to edit parameters
	for {
		action, editIndex, err := ReviewResults(results)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

		switch action {
		case ReviewActionContinue:
			// Proceed to output configuration
			// Break out of the review loop

		case ReviewActionEdit:
			// Re-collect params for selected template
			tmpl := templateMap[results[editIndex].TemplateName]

			fmt.Printf("\n%s\n", progressStyle.Render(
				fmt.Sprintf("━━━ Editing: %s ━━━", tmpl.Metadata.Title),
			))
			fmt.Printf("%s\n\n", tmpl.Metadata.Description)

			newParams, err := collectTemplateParams(tmpl)
			if err != nil {
				fmt.Printf("Error collecting parameters: %v\n", err)
				continue // Stay in review loop
			}

			// Re-render the template
			fmt.Printf("Re-rendering %s... ", tmpl.Metadata.Name)
			content, err := client.RenderTemplate(tmpl.Metadata.Name, newParams)
			if err != nil {
				fmt.Println(errorStyle.Render("failed"))
				results[editIndex].Error = err
			} else {
				fmt.Println(successStyle.Render("done"))
				results[editIndex].Content = content
				results[editIndex].Params = newParams
				results[editIndex].Error = nil
				if name, ok := newParams["name"]; ok {
					results[editIndex].ResourceName = fmt.Sprintf("%v", name)
				}
			}
			continue // Loop back to review

		case ReviewActionCancel:
			fmt.Println("Cancelled.")
			return
		}

		break // Exit review loop on continue
	}

	// Check for any successful renders
	successCount := 0
	for _, r := range results {
		if r.Error == nil {
			successCount++
		}
	}

	if successCount == 0 {
		fmt.Println(errorStyle.Render("\nNo successful renders to save!"))
		os.Exit(1)
	}

	fmt.Println(successStyle.Render(fmt.Sprintf("\n%d/%d ready to save", successCount, len(results))))

	// Build output config from flags or run interactive form
	var outputConfig OutputConfig

	// Check if output flags were explicitly set (non-default values or dry-run)
	if dryRun || outputDir != "/tmp" || singleFile || filenamePattern != "{{.template}}-{{.name}}.yaml" {
		// Use flag values
		outputConfig = OutputConfig{
			Directory:       outputDir,
			FilenamePattern: filenamePattern,
			SingleFile:      singleFile,
			DryRun:          dryRun,
		}
	} else {
		// Run interactive output form
		config, err := runOutputForm()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		if config == nil {
			fmt.Println("Save cancelled.")
			return
		}
		outputConfig = *config
	}

	// Write results using the output configuration
	if err := WriteResults(results, outputConfig); err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Failed to save: %v", err)))
		os.Exit(1)
	}
}

// selectTemplates displays a multi-select form for template selection
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

// collectAllParams collects parameters for all selected templates
func collectAllParams(selectedNames []string, templateMap map[string]*templates.ClaimTemplate) ([]TemplateParams, error) {
	var allParams []TemplateParams

	for i, name := range selectedNames {
		tmpl := templateMap[name]

		// Show progress header
		fmt.Printf("\n%s\n", progressStyle.Render(
			fmt.Sprintf("━━━ Configuring: %s (%d/%d) ━━━", tmpl.Metadata.Title, i+1, len(selectedNames)),
		))
		fmt.Printf("%s\n\n", tmpl.Metadata.Description)

		// Collect params for this template
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

// collectTemplateParams collects parameters for a single template
func collectTemplateParams(tmpl *templates.ClaimTemplate) (map[string]any, error) {
	params := make(map[string]any)
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

// renderAllTemplates renders all templates and returns results
func renderAllTemplates(client *templates.Client, allParams []TemplateParams) []RenderResult {
	var results []RenderResult

	for _, tp := range allParams {
		fmt.Printf("  Rendering %s... ", tp.TemplateName)

		content, err := client.RenderTemplate(tp.TemplateName, tp.Params)
		if err != nil {
			fmt.Println(errorStyle.Render("failed"))
			results = append(results, RenderResult{
				TemplateName: tp.TemplateName,
				Params:       tp.Params,
				Error:        err,
			})
			continue
		}

		// Extract resource name for filename
		resourceName := "output"
		if name, ok := tp.Params["name"]; ok {
			resourceName = fmt.Sprintf("%v", name)
		}

		fmt.Println(successStyle.Render("done"))
		results = append(results, RenderResult{
			TemplateName: tp.TemplateName,
			ResourceName: resourceName,
			Content:      content,
			Params:       tp.Params,
		})
	}

	return results
}

// runOutputForm runs the interactive output configuration form
func runOutputForm() (*OutputConfig, error) {
	var (
		saveFile        bool   = true
		outputMode      string = "separate"
		outputDirectory string = "/tmp"
		pattern         string = "{{.template}}-{{.name}}.yaml"
	)

	// First, ask if user wants to save
	saveForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Save to file?").
				Description("Save the rendered output to a file").
				Affirmative("Yes").
				Negative("No").
				Value(&saveFile),
		),
	)

	if err := saveForm.Run(); err != nil {
		return nil, err
	}

	if !saveFile {
		return nil, nil
	}

	// Ask for output configuration
	configForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Output directory").
				Description("Where to save rendered files").
				Value(&outputDirectory),

			huh.NewSelect[string]().
				Title("Output mode").
				Description("How should resources be organized?").
				Options(
					huh.NewOption("Separate files (one per resource)", "separate"),
					huh.NewOption("Single file (combined with ---)", "single"),
				).
				Value(&outputMode),
		),
	)

	if err := configForm.Run(); err != nil {
		return nil, err
	}

	// For separate files, ask for filename pattern
	if outputMode == "separate" {
		patternForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Filename pattern").
					Description("Pattern for output filenames").
					Options(
						huh.NewOption("{{template}}-{{name}}.yaml (default)", "{{.template}}-{{.name}}.yaml"),
						huh.NewOption("{{name}}.yaml", "{{.name}}.yaml"),
						huh.NewOption("Custom", "custom"),
					).
					Value(&pattern),
			),
		)

		if err := patternForm.Run(); err != nil {
			return nil, err
		}

		// If custom, prompt for pattern
		if pattern == "custom" {
			customForm := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Custom filename pattern").
						Description("Use {{.template}} and {{.name}} as placeholders").
						Placeholder("{{.template}}-{{.name}}.yaml").
						Value(&pattern),
				),
			)

			if err := customForm.Run(); err != nil {
				return nil, err
			}
		}
	}

	return &OutputConfig{
		Directory:       outputDirectory,
		FilenamePattern: pattern,
		SingleFile:      outputMode == "single",
		DryRun:          false,
	}, nil
}

// promptAPIURL prompts the user to confirm or change the API URL
func promptAPIURL(currentURL string) (string, error) {
	apiURLInput := currentURL
	apiForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("API URL").
				Description("Confirm or change the API endpoint").
				Value(&apiURLInput),
		),
	)

	if err := apiForm.Run(); err != nil {
		return "", err
	}
	return apiURLInput, nil
}

// createField creates the appropriate huh field based on parameter type
func createField(p templates.Parameter, value *string) huh.Field {
	title := p.Title
	if p.Required {
		title += " *"
	}

	description := p.Description
	if p.Pattern != "" {
		description += fmt.Sprintf(" (pattern: %s)", p.Pattern)
	}

	// If parameter has enum values, use Select
	if len(p.Enum) > 0 {
		var options []huh.Option[string]

		// Add Random option if allowed
		if p.AllowRandom {
			options = append(options, huh.NewOption(randomMarker, randomMarker))
		}

		for _, e := range p.Enum {
			enumStr := fmt.Sprintf("%v", e)
			options = append(options, huh.NewOption(enumStr, enumStr))
		}

		return huh.NewSelect[string]().
			Title(title).
			Description(description).
			Options(options...).
			Value(value)
	}

	// Handle different types
	switch p.Type {
	case "boolean":
		return huh.NewSelect[string]().
			Title(title).
			Description(description).
			Options(
				huh.NewOption("true", "true"),
				huh.NewOption("false", "false"),
			).
			Value(value)

	case "integer":
		return huh.NewInput().
			Title(title).
			Description(description).
			Placeholder(fmt.Sprintf("default: %v", p.Default)).
			Value(value).
			Validate(func(s string) error {
				if s == "" {
					return nil
				}
				if _, err := strconv.Atoi(s); err != nil {
					return fmt.Errorf("must be a number")
				}
				return nil
			})

	default: // string
		return huh.NewInput().
			Title(title).
			Description(description).
			Placeholder(fmt.Sprintf("default: %v", p.Default)).
			Value(value)
	}
}
