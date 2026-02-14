package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"
	"github.com/stuttgart-things/claims/internal/sops"
	"github.com/stuttgart-things/claims/internal/templates"
)

// runEncryptInteractive runs the encrypt command in interactive mode
func runEncryptInteractive(config *EncryptConfig) error {
	// 1. Check SOPS prerequisites
	fmt.Println(progressStyle.Render("Checking SOPS prerequisites..."))
	recipients, err := sops.CheckSOPSAvailable()
	if err != nil {
		return fmt.Errorf("SOPS prerequisites: %w", err)
	}
	fmt.Println(successStyle.Render("SOPS available (age encryption)"))

	// 2. Prompt/confirm API URL
	confirmedURL, err := promptAPIURL(config.APIUrl)
	if err != nil {
		return fmt.Errorf("API URL prompt: %w", err)
	}
	config.APIUrl = confirmedURL
	fmt.Printf("\nConnecting to API: %s\n\n", config.APIUrl)

	// 3. Fetch templates from API
	client := templates.NewClient(config.APIUrl)
	templateList, err := client.FetchTemplates()
	if err != nil {
		return fmt.Errorf("fetching templates: %w", err)
	}
	fmt.Printf("Loaded %d templates from API\n\n", len(templateList))

	// Build template map
	templateMap := make(map[string]*templates.ClaimTemplate)
	for i, t := range templateList {
		templateMap[t.Metadata.Name] = &templateList[i]
	}

	// 4. Template selection (single-select)
	var selectedName string
	if config.Template != "" {
		if _, exists := templateMap[config.Template]; !exists {
			return fmt.Errorf("template not found: %s", config.Template)
		}
		selectedName = config.Template
	} else {
		var options []huh.Option[string]
		for _, t := range templateList {
			label := fmt.Sprintf("%s - %s", t.Metadata.Name, t.Metadata.Title)
			options = append(options, huh.NewOption(label, t.Metadata.Name))
		}

		selectForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Select template for secret").
					Description("Choose which template's parameters to use for secret values").
					Options(options...).
					Value(&selectedName),
			),
		)

		if err := selectForm.Run(); err != nil {
			return fmt.Errorf("template selection: %w", err)
		}
	}

	tmpl := templateMap[selectedName]
	fmt.Printf("\nSelected template: %s\n", selectedName)

	// 5. Collect secret metadata (name + namespace)
	secretName := config.SecretName
	secretNamespace := config.SecretNamespace

	if secretName == "" || secretNamespace == "" {
		metaForm := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Secret name").
					Description("Kubernetes Secret resource name").
					Placeholder("my-app-secret").
					Value(&secretName).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("secret name is required")
						}
						return nil
					}),

				huh.NewInput().
					Title("Secret namespace").
					Description("Kubernetes namespace for the Secret").
					Placeholder("default").
					Value(&secretNamespace).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("namespace is required")
						}
						return nil
					}),
			),
		)

		if err := metaForm.Run(); err != nil {
			return fmt.Errorf("secret metadata: %w", err)
		}
	}

	// 6. Collect secret values from template parameters
	stringData, err := collectSecretValues(tmpl)
	if err != nil {
		return fmt.Errorf("collecting secret values: %w", err)
	}

	if len(stringData) == 0 {
		return fmt.Errorf("no secret values provided")
	}

	// 7. Generate Secret YAML
	fmt.Println(progressStyle.Render("\nGenerating Kubernetes Secret YAML..."))
	secretYAML, err := sops.GenerateSecretYAML(sops.SecretData{
		Name:       secretName,
		Namespace:  secretNamespace,
		StringData: stringData,
	})
	if err != nil {
		return fmt.Errorf("generating secret YAML: %w", err)
	}

	// 8. Preview (pre-encryption) + confirm
	fmt.Println(progressStyle.Render("\nSecret YAML (pre-encryption):"))
	fmt.Println(yamlStyle.Render(string(secretYAML)))

	var confirm bool
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Encrypt this secret?").
				Description("The secret will be encrypted with SOPS (age) before saving").
				Affirmative("Yes, encrypt").
				Negative("Cancel").
				Value(&confirm),
		),
	)

	if err := confirmForm.Run(); err != nil {
		return fmt.Errorf("confirmation: %w", err)
	}

	if !confirm {
		fmt.Println("Cancelled.")
		return nil
	}

	// 9. Encrypt
	fmt.Println(progressStyle.Render("Encrypting with SOPS..."))
	encrypted, err := sops.Encrypt(secretYAML, recipients)
	if err != nil {
		return fmt.Errorf("encrypting: %w", err)
	}
	fmt.Println(successStyle.Render("Encrypted successfully"))

	// Build result
	result := &EncryptResult{
		TemplateName:    selectedName,
		SecretName:      secretName,
		SecretNamespace: secretNamespace,
		Content:         string(encrypted),
	}

	// 10. Dry run check
	if config.DryRun {
		return printEncryptDryRun(result, config)
	}

	// 11. Output config — ask where to save
	var outputDir string
	var useGit bool

	destChoice, err := runDestinationChoice()
	if err != nil {
		return fmt.Errorf("destination choice: %w", err)
	}
	useGit = destChoice.useGit

	if useGit {
		// For git, pick an output directory inside the repo
		for {
			selectedDir, err := selectDirectory(".")
			if err != nil {
				return fmt.Errorf("directory selection: %w", err)
			}
			outputDir = selectedDir

			// Validate it's in a git repo
			if _, err := findRepoRoot(outputDir); err != nil {
				fmt.Println(errorStyle.Render("Error: Output directory is not in a git repository"))
				continue
			}
			break
		}
	} else {
		selectedDir, err := selectDirectory(".")
		if err != nil {
			return fmt.Errorf("directory selection: %w", err)
		}
		outputDir = selectedDir
	}

	// Generate filename
	filename, err := generateEncryptFilename(config.FilenamePattern, secretName, selectedName)
	if err != nil {
		return fmt.Errorf("generating filename: %w", err)
	}

	outputPath := filepath.Join(outputDir, filename)

	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Write encrypted file
	if err := os.WriteFile(outputPath, encrypted, 0644); err != nil {
		return fmt.Errorf("writing encrypted file: %w", err)
	}
	result.OutputPath = outputPath
	fmt.Println(successStyle.Render(fmt.Sprintf("Saved: %s", outputPath)))

	// 12. Update registry
	updateRegistryForEncrypt(result, outputDir)

	// 13. Git operations
	if useGit {
		if config.GitConfig == nil {
			gitConfig, err := runGitDetailsForm(destChoice.createPR)
			if err != nil {
				return fmt.Errorf("git options: %w", err)
			}
			config.GitConfig = gitConfig

			if destChoice.createPR {
				prConfig, err := runPROptionsForm()
				if err != nil {
					return fmt.Errorf("PR options: %w", err)
				}
				config.PRConfig = prConfig
			}
		}

		if err := executeEncryptGitOperations(result, config); err != nil {
			return fmt.Errorf("git operations: %w", err)
		}
	}

	return nil
}

// collectSecretValues collects secret values for each template parameter.
// Hidden parameters use password-mode input.
func collectSecretValues(tmpl *templates.ClaimTemplate) (map[string]string, error) {
	if len(tmpl.Spec.Parameters) == 0 {
		return nil, fmt.Errorf("template has no parameters")
	}

	paramValues := make(map[string]*string)
	var formGroups []*huh.Group
	var currentFields []huh.Field

	for _, p := range tmpl.Spec.Parameters {
		defaultVal := ""
		if p.Default != nil {
			defaultVal = fmt.Sprintf("%v", p.Default)
		}
		paramValues[p.Name] = &defaultVal

		title := p.Title
		if title == "" {
			title = p.Name
		}
		if p.Required {
			title += " *"
		}

		description := p.Description
		if description == "" {
			description = fmt.Sprintf("Value for secret key %q", p.Name)
		}

		var field huh.Field

		if len(p.Enum) > 0 {
			// Enum parameters use a select
			var options []huh.Option[string]
			for _, e := range p.Enum {
				enumStr := fmt.Sprintf("%v", e)
				options = append(options, huh.NewOption(enumStr, enumStr))
			}
			field = huh.NewSelect[string]().
				Title(title).
				Description(description).
				Options(options...).
				Value(paramValues[p.Name])
		} else if p.Hidden {
			// Hidden parameters use password echo mode
			field = huh.NewInput().
				Title(title).
				Description(description).
				EchoMode(huh.EchoModePassword).
				Value(paramValues[p.Name]).
				Validate(func(s string) error {
					if p.Required && s == "" {
						return fmt.Errorf("%s is required", p.Name)
					}
					return nil
				})
		} else {
			// Normal parameters use standard input
			field = huh.NewInput().
				Title(title).
				Description(description).
				Placeholder(fmt.Sprintf("default: %v", p.Default)).
				Value(paramValues[p.Name]).
				Validate(func(s string) error {
					if p.Required && s == "" {
						return fmt.Errorf("%s is required", p.Name)
					}
					return nil
				})
		}

		currentFields = append(currentFields, field)

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

	// Build stringData from collected values
	stringData := make(map[string]string)
	for _, p := range tmpl.Spec.Parameters {
		val := *paramValues[p.Name]
		if val != "" {
			stringData[p.Name] = val
		}
	}

	return stringData, nil
}

// generateEncryptFilename creates a filename from pattern, secret name, and template name
func generateEncryptFilename(pattern, secretName, templateName string) (string, error) {
	tmpl, err := template.New("filename").Parse(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid filename pattern: %w", err)
	}

	data := map[string]string{
		"name":     secretName,
		"template": templateName,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing filename template: %w", err)
	}

	return buf.String(), nil
}

// printEncryptDryRun shows what would be written without actually writing files
func printEncryptDryRun(result *EncryptResult, config *EncryptConfig) error {
	fmt.Println("\n=== DRY RUN - No files written ===")

	filename, err := generateEncryptFilename(config.FilenamePattern, result.SecretName, result.TemplateName)
	if err != nil {
		filename = fmt.Sprintf("%s-secret.enc.yaml", result.SecretName)
	}

	path := filepath.Join(config.OutputDir, filename)
	fmt.Printf("Would write: %s\n", path)
	fmt.Printf("  Template:   %s\n", result.TemplateName)
	fmt.Printf("  Secret:     %s/%s\n", result.SecretNamespace, result.SecretName)
	fmt.Println()

	// Show truncated encrypted content
	lines := strings.Split(result.Content, "\n")
	preview := result.Content
	if len(lines) > 20 {
		preview = strings.Join(lines[:20], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-20)
	}
	fmt.Println(yamlStyle.Render(preview))

	return nil
}
