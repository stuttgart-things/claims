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
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			MarginBottom(1)

	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("42"))

	yamlStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))
)

// runInteractiveRender runs the interactive render flow
func runInteractiveRender(client *templates.Client) {
	// Fetch templates from API
	templateList, err := client.FetchTemplates()
	if err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Failed to fetch templates: %v", err)))
		os.Exit(1)
	}

	fmt.Printf("Loaded %d templates from API\n\n", len(templateList))

	// Build template map and options for selection
	templateMap := make(map[string]*templates.ClaimTemplate)
	var templateOptions []huh.Option[string]

	for i, t := range templateList {
		templateMap[t.Metadata.Name] = &templateList[i]
		label := fmt.Sprintf("%s - %s", t.Metadata.Name, t.Metadata.Title)
		templateOptions = append(templateOptions, huh.NewOption(label, t.Metadata.Name))
	}

	// Step 1: Select template
	var selectedTemplate string
	selectForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a template").
				Description("Choose which claim template to render").
				Options(templateOptions...).
				Value(&selectedTemplate),
		),
	)

	if err := selectForm.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	tmpl := templateMap[selectedTemplate]
	fmt.Printf("\n%s\n", titleStyle.Render(tmpl.Metadata.Title))
	fmt.Printf("%s\n\n", tmpl.Metadata.Description)

	// Step 2: Build dynamic form based on template parameters
	params := make(map[string]interface{})
	paramValues := make(map[string]*string)

	// Create form fields for each parameter
	var formGroups []*huh.Group
	var currentFields []huh.Field

	for _, p := range tmpl.Spec.Parameters {
		// Create a string pointer to hold the value (including hidden params)
		defaultVal := ""
		if p.Default != nil {
			defaultVal = fmt.Sprintf("%v", p.Default)
		}
		paramValues[p.Name] = &defaultVal

		// Skip hidden parameters - they use their default value
		if p.Hidden {
			continue
		}

		field := createField(p, paramValues[p.Name])
		if field != nil {
			currentFields = append(currentFields, field)
		}

		// Group fields (max 5 per group for better UX)
		if len(currentFields) >= 5 {
			formGroups = append(formGroups, huh.NewGroup(currentFields...))
			currentFields = nil
		}
	}

	// Add remaining fields as final group
	if len(currentFields) > 0 {
		formGroups = append(formGroups, huh.NewGroup(currentFields...))
	}

	// Run the parameter form
	if len(formGroups) > 0 {
		paramForm := huh.NewForm(formGroups...)
		if err := paramForm.Run(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Resolve random selections and collect non-empty values
	for _, p := range tmpl.Spec.Parameters {
		strVal := *paramValues[p.Name]
		if strVal == "" {
			continue
		}
		// If user selected random, pick a random enum value
		if strVal == randomMarker && len(p.Enum) > 0 {
			randomIdx := rand.Intn(len(p.Enum))
			strVal = p.Enum[randomIdx]
			fmt.Printf("Random selection for %s: %s\n", p.Name, strVal)
		}
		params[p.Name] = strVal
	}

	// Step 3: Confirm and render (default: Yes)
	confirm := true
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Render the claim?").
				Description("This will call the API to generate YAML").
				Affirmative("Yes, render it").
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

	// Call API to render
	fmt.Println("\nCalling API to render...")

	result, err := client.RenderTemplate(selectedTemplate, params)
	if err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Render failed: %v", err)))
		os.Exit(1)
	}

	fmt.Println(successStyle.Render("\nRendered successfully!"))
	fmt.Println(yamlStyle.Render(result))

	// Get resource name for filename generation
	resourceName := "output"
	if name, ok := params["name"]; ok {
		resourceName = fmt.Sprintf("%v", name)
	}

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

	// Write using the output configuration
	renderResult := RenderResult{
		TemplateName: tmpl.Metadata.Name,
		ResourceName: resourceName,
		Content:      result,
		Params:       params,
	}

	if err := WriteResults([]RenderResult{renderResult}, outputConfig); err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Failed to save: %v", err)))
		os.Exit(1)
	}
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
