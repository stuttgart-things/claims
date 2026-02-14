package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/stuttgart-things/claims/internal/params"
	"github.com/stuttgart-things/claims/internal/sops"
	"github.com/stuttgart-things/claims/internal/templates"
)

// runEncryptNonInteractive runs the encrypt command in non-interactive mode
func runEncryptNonInteractive(config *EncryptConfig) error {
	// Validate required inputs
	if config.Template == "" {
		return fmt.Errorf("--template is required in non-interactive mode")
	}
	if config.SecretName == "" {
		return fmt.Errorf("--name is required in non-interactive mode")
	}
	if config.SecretNamespace == "" {
		return fmt.Errorf("--namespace is required in non-interactive mode")
	}
	if config.ParamsFile == "" && len(config.InlineParamsRaw) == 0 {
		return fmt.Errorf("--params-file or --param is required in non-interactive mode")
	}

	// Check SOPS prerequisites
	fmt.Println("Checking SOPS prerequisites...")
	recipients, err := sops.CheckSOPSAvailable()
	if err != nil {
		return fmt.Errorf("SOPS prerequisites: %w", err)
	}
	fmt.Println("SOPS available (age encryption)")

	// Fetch templates to validate
	fmt.Printf("Connecting to API: %s\n", config.APIUrl)
	client := templates.NewClient(config.APIUrl)
	available, err := client.FetchTemplates()
	if err != nil {
		return fmt.Errorf("fetching templates: %w", err)
	}

	var tmpl *templates.ClaimTemplate
	for i, t := range available {
		if t.Metadata.Name == config.Template {
			tmpl = &available[i]
			break
		}
	}
	if tmpl == nil {
		return fmt.Errorf("template not found: %s", config.Template)
	}

	// Parse parameters
	var mergedParams map[string]any

	if config.ParamsFile != "" {
		pf, err := params.ParseFile(config.ParamsFile)
		if err != nil {
			return fmt.Errorf("parsing params file: %w", err)
		}
		// Use first template's params or top-level params
		if len(pf.Templates) > 0 {
			mergedParams = pf.Templates[0].Parameters
		} else {
			mergedParams = pf.Parameters
		}
	}

	if mergedParams == nil {
		mergedParams = make(map[string]any)
	}

	// Parse and merge inline params
	inlineP, err := params.ParseInlineParams(config.InlineParamsRaw)
	if err != nil {
		return fmt.Errorf("parsing inline params: %w", err)
	}
	mergedParams = params.MergeParams(mergedParams, inlineP)

	// Build stringData from params
	stringData := make(map[string]string)
	for k, v := range mergedParams {
		stringData[k] = fmt.Sprintf("%v", v)
	}

	if len(stringData) == 0 {
		return fmt.Errorf("no secret values provided")
	}

	// Generate Secret YAML
	fmt.Println("Generating Kubernetes Secret YAML...")
	secretYAML, err := sops.GenerateSecretYAML(sops.SecretData{
		Name:       config.SecretName,
		Namespace:  config.SecretNamespace,
		StringData: stringData,
	})
	if err != nil {
		return fmt.Errorf("generating secret YAML: %w", err)
	}

	// Encrypt
	fmt.Println("Encrypting with SOPS...")
	encrypted, err := sops.Encrypt(secretYAML, recipients)
	if err != nil {
		return fmt.Errorf("encrypting: %w", err)
	}
	fmt.Println("Encrypted successfully")

	result := &EncryptResult{
		TemplateName:    config.Template,
		SecretName:      config.SecretName,
		SecretNamespace: config.SecretNamespace,
		Content:         string(encrypted),
	}

	// Dry run
	if config.DryRun {
		return printEncryptDryRun(result, config)
	}

	// Write encrypted file
	filename, err := generateEncryptFilename(config.FilenamePattern, config.SecretName, config.Template)
	if err != nil {
		return fmt.Errorf("generating filename: %w", err)
	}

	if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	outputPath := filepath.Join(config.OutputDir, filename)
	if err := os.WriteFile(outputPath, encrypted, 0644); err != nil {
		return fmt.Errorf("writing encrypted file: %w", err)
	}
	result.OutputPath = outputPath
	fmt.Printf("Saved: %s\n", outputPath)

	// Update registry
	updateRegistryForEncrypt(result, config.OutputDir)

	// Git operations
	if config.GitConfig != nil {
		if err := executeEncryptGitOperations(result, config); err != nil {
			return fmt.Errorf("git operations: %w", err)
		}
	}

	return nil
}
