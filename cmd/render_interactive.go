package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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

// runInteractive runs the render command in interactive mode
func runInteractive(config *RenderConfig) error {
	client := templates.NewClient(config.APIUrl)
	return runInteractiveRender(client, config)
}

// runInteractiveRender runs the interactive render flow
func runInteractiveRender(client *templates.Client, config *RenderConfig) error {
	// Fetch templates from API
	templateList, err := client.FetchTemplates()
	if err != nil {
		return fmt.Errorf("failed to fetch templates: %w", err)
	}

	fmt.Printf("Loaded %d templates from API\n\n", len(templateList))

	// Build template map
	templateMap := make(map[string]*templates.ClaimTemplate)
	for i, t := range templateList {
		templateMap[t.Metadata.Name] = &templateList[i]
	}

	// Select templates (multi-select or use config values)
	var selectedNames []string
	if len(config.Templates) > 0 {
		// Validate provided template names
		for _, name := range config.Templates {
			if _, exists := templateMap[name]; !exists {
				return fmt.Errorf("template not found: %s", name)
			}
		}
		selectedNames = config.Templates
	} else {
		// Interactive multi-select
		selectedNames, err = selectTemplates(templateList)
		if err != nil {
			return fmt.Errorf("selecting templates: %w", err)
		}
	}

	fmt.Printf("\nSelected %d template(s): %v\n", len(selectedNames), selectedNames)

	// Collect parameters for each selected template
	allParams, err := collectAllParams(selectedNames, templateMap)
	if err != nil {
		return fmt.Errorf("collecting parameters: %w", err)
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
		return fmt.Errorf("confirmation form: %w", err)
	}

	if !confirm {
		fmt.Println("Cancelled.")
		return nil
	}

	// Render all templates
	fmt.Println("\nRendering templates...")
	results := renderAllTemplates(client, allParams)

	// Review loop - allows going back to edit parameters
	for {
		action, editIndex, err := ReviewResults(results)
		if err != nil {
			return fmt.Errorf("review: %w", err)
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
			return nil
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
		return fmt.Errorf("no successful renders to save")
	}

	fmt.Println(successStyle.Render(fmt.Sprintf("\n%d/%d ready to save", successCount, len(results))))

	// Build output config from flags or run interactive form
	var outputConfig OutputConfig

	// Check if output flags were explicitly set (non-default values or dry-run)
	if config.DryRun || config.OutputDir != "." || config.SingleFile || config.FilenamePattern != "{{.template}}-{{.name}}.yaml" {
		// Use flag values
		outputConfig = OutputConfig{
			Directory:       config.OutputDir,
			FilenamePattern: config.FilenamePattern,
			SingleFile:      config.SingleFile,
			DryRun:          config.DryRun,
		}
	} else {
		// Get example template and name for filename preview
		var exampleTemplate, exampleName string
		for _, r := range results {
			if r.Error == nil {
				exampleTemplate = r.TemplateName
				exampleName = r.ResourceName
				break
			}
		}

		// Loop to allow going back from git validation to destination choice
		for {
			// First ask: git or save locally?
			destChoice, err := runDestinationChoice()
			if err != nil {
				return fmt.Errorf("destination choice: %w", err)
			}

			// Run interactive output form with git validation if needed
			formConfig, goBack, err := runOutputFormWithValidation(destChoice.useGit, successCount, exampleTemplate, exampleName)
			if err != nil {
				return fmt.Errorf("output configuration: %w", err)
			}
			if goBack {
				// User wants to go back and change destination choice
				continue
			}
			if formConfig == nil {
				fmt.Println("Save cancelled.")
				return nil
			}
			outputConfig = *formConfig

			// If git was chosen, collect git options now
			if destChoice.useGit {
				gitConfig, err := runGitDetailsForm(destChoice.createPR)
				if err != nil {
					return fmt.Errorf("git options: %w", err)
				}
				config.GitConfig = gitConfig

				// If PR was chosen, collect PR options
				if destChoice.createPR {
					prConfig, err := runPROptionsForm()
					if err != nil {
						return fmt.Errorf("PR options: %w", err)
					}
					config.PRConfig = prConfig
				}
			}
			break
		}
	}

	// Write results using the output configuration
	if err := WriteResults(results, outputConfig); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	// Execute git operations if configured (and not dry-run)
	if !outputConfig.DryRun && config.GitConfig != nil {
		// Update config with the actual output directory used
		config.OutputDir = outputConfig.Directory

		if err := executeGitOperations(results, config); err != nil {
			return fmt.Errorf("git operations: %w", err)
		}
	}

	return nil
}

// destinationChoice represents the user's choice for where to save files
type destinationChoice struct {
	useGit   bool
	createPR bool
}

// runDestinationChoice asks the user whether to save locally, commit to git, or create a PR
func runDestinationChoice() (destinationChoice, error) {
	var destination string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Where to save?").
				Description("Choose how to save the rendered files").
				Options(
					huh.NewOption("Save locally only", "local"),
					huh.NewOption("Commit to git repository", "git"),
					huh.NewOption("Commit, push & create PR", "pr"),
				).
				Value(&destination),
		),
	)

	if err := form.Run(); err != nil {
		return destinationChoice{}, err
	}

	return destinationChoice{
		useGit:   destination == "git" || destination == "pr",
		createPR: destination == "pr",
	}, nil
}

// getSubfolders returns a list of subdirectories in the given path
func getSubfolders(basePath string) ([]string, error) {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return nil, err
	}

	var folders []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			folders = append(folders, entry.Name())
		}
	}
	sort.Strings(folders)
	return folders, nil
}

// selectDirectory runs an interactive directory picker
// Returns the selected directory path
func selectDirectory(startPath string) (string, error) {
	currentPath := startPath
	if currentPath == "" {
		currentPath = "."
	}

	// Resolve to absolute path for display
	absPath, err := filepath.Abs(currentPath)
	if err != nil {
		absPath = currentPath
	}

	for {
		subfolders, err := getSubfolders(currentPath)
		if err != nil {
			// If we can't read the directory, fall back to manual input
			return manualDirectoryInput(currentPath)
		}

		// Build options
		var options []huh.Option[string]

		// Option to use current directory
		displayPath := currentPath
		if currentPath == "." {
			displayPath = ". (current directory)"
		}
		options = append(options, huh.NewOption(fmt.Sprintf("✓ Use this directory: %s", displayPath), "__use_current__"))

		// Option to go to parent directory (if not at root)
		if currentPath != "/" && currentPath != "." {
			options = append(options, huh.NewOption("⬆ Parent directory", "__parent__"))
		} else if currentPath == "." {
			options = append(options, huh.NewOption("⬆ Parent directory", "__parent__"))
		}

		// List subfolders
		for _, folder := range subfolders {
			options = append(options, huh.NewOption(fmt.Sprintf("📁 %s", folder), folder))
		}

		// Option to create new folder
		options = append(options, huh.NewOption("➕ Create new folder...", "__create__"))

		// Option to enter path manually
		options = append(options, huh.NewOption("✏️  Enter path manually...", "__manual__"))

		var choice string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select output directory").
					Description(fmt.Sprintf("Current: %s", absPath)).
					Options(options...).
					Value(&choice),
			),
		)

		if err := form.Run(); err != nil {
			return "", err
		}

		switch choice {
		case "__use_current__":
			return currentPath, nil

		case "__parent__":
			if currentPath == "." {
				currentPath = ".."
			} else {
				currentPath = filepath.Dir(currentPath)
			}
			absPath, _ = filepath.Abs(currentPath)

		case "__create__":
			newFolder, created, err := createNewFolder(currentPath)
			if err != nil {
				return "", err
			}
			if created {
				return newFolder, nil
			}
			// User cancelled, continue loop

		case "__manual__":
			return manualDirectoryInput(currentPath)

		default:
			// User selected a subfolder
			currentPath = filepath.Join(currentPath, choice)
			absPath, _ = filepath.Abs(currentPath)
		}
	}
}

// createNewFolder prompts for a folder name and creates it
// Returns the full path, whether it was created, and any error
func createNewFolder(basePath string) (string, bool, error) {
	var folderName string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("New folder name").
				Description(fmt.Sprintf("Will be created in: %s", basePath)).
				Value(&folderName).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("folder name cannot be empty")
					}
					if strings.ContainsAny(s, "/\\:*?\"<>|") {
						return fmt.Errorf("folder name contains invalid characters")
					}
					return nil
				}),
		),
	)

	if err := form.Run(); err != nil {
		return "", false, err
	}

	if folderName == "" {
		return "", false, nil
	}

	newPath := filepath.Join(basePath, folderName)

	// Check if folder already exists
	if _, err := os.Stat(newPath); err == nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Folder '%s' already exists", folderName)))
		var useExisting bool
		confirmForm := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Use existing folder?").
					Value(&useExisting),
			),
		)
		if err := confirmForm.Run(); err != nil {
			return "", false, err
		}
		if useExisting {
			return newPath, true, nil
		}
		return "", false, nil
	}

	// Create the folder
	if err := os.MkdirAll(newPath, 0755); err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Failed to create folder: %v", err)))
		return "", false, nil
	}

	fmt.Println(successStyle.Render(fmt.Sprintf("Created folder: %s", newPath)))
	return newPath, true, nil
}

// manualDirectoryInput allows the user to type a directory path manually
func manualDirectoryInput(defaultPath string) (string, error) {
	var outputDir string = defaultPath

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Output directory").
				Description("Enter the path manually").
				Value(&outputDir),
		),
	)

	if err := form.Run(); err != nil {
		return "", err
	}

	// Check if directory exists, offer to create if not
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		var createDir bool
		confirmForm := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Directory '%s' doesn't exist. Create it?", outputDir)).
					Value(&createDir),
			),
		)
		if err := confirmForm.Run(); err != nil {
			return "", err
		}
		if createDir {
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				return "", fmt.Errorf("failed to create directory: %w", err)
			}
			fmt.Println(successStyle.Render(fmt.Sprintf("Created directory: %s", outputDir)))
		}
	}

	return outputDir, nil
}

// runOutputFormWithValidation runs the output form and validates git repo if needed
// Returns OutputConfig, shouldGoBack (to change destination), and error
func runOutputFormWithValidation(requireGitRepo bool, resultCount int, exampleTemplate, exampleName string) (*OutputConfig, bool, error) {
	var (
		outputDirectory = "."
		outputMode      = "separate"
		pattern         = "{{.template}}-{{.name}}.yaml"
	)

	// Ask for output directory with validation using the directory picker
	for {
		selectedDir, err := selectDirectory(".")
		if err != nil {
			return nil, false, err
		}
		outputDirectory = selectedDir

		// Validate git repo if required
		if requireGitRepo {
			_, err := findRepoRoot(outputDirectory)
			if err != nil {
				fmt.Println(errorStyle.Render("Error: Output directory is not in a git repository"))
				fmt.Println()

				var retryChoice string
				retryForm := huh.NewForm(
					huh.NewGroup(
						huh.NewSelect[string]().
							Title("What would you like to do?").
							Options(
								huh.NewOption("Choose a different directory", "retry"),
								huh.NewOption("Go back and save locally instead", "goback"),
								huh.NewOption("Cancel", "cancel"),
							).
							Value(&retryChoice),
					),
				)
				if err := retryForm.Run(); err != nil {
					return nil, false, err
				}

				switch retryChoice {
				case "retry":
					continue
				case "goback":
					return nil, true, nil
				default: // cancel
					return nil, false, nil
				}
			}
		}
		break
	}

	// Only ask for output mode if rendering multiple files
	if resultCount > 1 {
		modeForm := huh.NewForm(
			huh.NewGroup(
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

		if err := modeForm.Run(); err != nil {
			return nil, false, err
		}
	}

	// For separate files, ask for filename pattern
	if outputMode == "separate" {
		// Build example filename for preview
		exampleDefault := fmt.Sprintf("%s-%s.yaml", exampleTemplate, exampleName)
		exampleNameOnly := fmt.Sprintf("%s.yaml", exampleName)

		patternForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Filename pattern").
					Description("Pattern for output filenames").
					Options(
						huh.NewOption(fmt.Sprintf("%s (default)", exampleDefault), "{{.template}}-{{.name}}.yaml"),
						huh.NewOption(exampleNameOnly, "{{.name}}.yaml"),
						huh.NewOption("Custom", "custom"),
					).
					Value(&pattern),
			),
		)

		if err := patternForm.Run(); err != nil {
			return nil, false, err
		}

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
				return nil, false, err
			}
		}
	}

	return &OutputConfig{
		Directory:       outputDirectory,
		FilenamePattern: pattern,
		SingleFile:      outputMode == "single",
		DryRun:          false,
	}, false, nil
}

// runGitDetailsForm prompts for git commit details (branch, message, push)
// If createPR is true, push is implied and user won't be asked about it
func runGitDetailsForm(createPR bool) (*GitConfig, error) {
	gitConfig := &GitConfig{
		Commit: true,
		Push:   createPR, // PR implies push
		Remote: "origin",
	}

	// If creating a PR, we need a new branch (can't PR from main to main)
	if createPR {
		var branchName string
		branchForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Branch name").
					Description("New branch for the PR (required for PR creation)").
					Placeholder("feature/my-changes").
					Value(&branchName).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("branch name required for PR")
						}
						return nil
					}),
			),
		)

		if err := branchForm.Run(); err != nil {
			return nil, err
		}

		gitConfig.CreateBranch = true
		gitConfig.Branch = branchName
	} else {
		// Not creating PR - ask about git action
		var gitAction string

		actionForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Git commit options").
					Description("How should the files be committed?").
					Options(
						huh.NewOption("Commit to current branch", "commit"),
						huh.NewOption("Commit to new branch", "branch"),
						huh.NewOption("Commit and push", "push"),
					).
					Value(&gitAction),
			),
		)

		if err := actionForm.Run(); err != nil {
			return nil, err
		}

		// If creating new branch, ask for branch name
		if gitAction == "branch" {
			var branchName string
			branchForm := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Branch name").
						Description("Name for the new branch").
						Placeholder("feature/my-changes").
						Value(&branchName).
						Validate(func(s string) error {
							if s == "" {
								return fmt.Errorf("branch name required")
							}
							return nil
						}),
				),
			)

			if err := branchForm.Run(); err != nil {
				return nil, err
			}

			gitConfig.CreateBranch = true
			gitConfig.Branch = branchName
		}

		// If pushing, set push flag
		if gitAction == "push" {
			gitConfig.Push = true
		}
	}

	// Ask for commit message
	var commitMessage string
	msgForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Commit message").
				Description("Leave empty for auto-generated message").
				Placeholder("Add rendered claims").
				Value(&commitMessage),
		),
	)

	if err := msgForm.Run(); err != nil {
		return nil, err
	}

	gitConfig.Message = commitMessage

	return gitConfig, nil
}

// runPROptionsForm prompts for PR details (title, description, labels, base branch)
func runPROptionsForm() (*PRConfig, error) {
	var (
		prTitle       string
		prDescription string
		prLabels      string
		prBase        string = "main"
	)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("PR Title").
				Description("Leave empty for auto-generated title").
				Placeholder("Add rendered claims").
				Value(&prTitle),

			huh.NewText().
				Title("PR Description").
				Description("Leave empty for auto-generated description").
				Value(&prDescription).
				CharLimit(1000),

			huh.NewInput().
				Title("Labels").
				Description("Comma-separated labels (e.g., infrastructure,automated)").
				Placeholder("infrastructure,automated").
				Value(&prLabels),

			huh.NewInput().
				Title("Base branch").
				Description("Target branch for the PR").
				Value(&prBase),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	// Parse labels
	var labels []string
	if prLabels != "" {
		for _, l := range strings.Split(prLabels, ",") {
			trimmed := strings.TrimSpace(l)
			if trimmed != "" {
				labels = append(labels, trimmed)
			}
		}
	}

	return &PRConfig{
		Create:      true,
		Title:       prTitle,
		Description: prDescription,
		Labels:      labels,
		BaseBranch:  prBase,
	}, nil
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
