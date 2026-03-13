package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/stuttgart-things/claims/internal/params"
	"github.com/stuttgart-things/claims/internal/sops"
	"github.com/stuttgart-things/claims/internal/templates"
)

// SecretRenderResult holds the result of generating and encrypting a secret
type SecretRenderResult struct {
	SecretName      string
	SecretNamespace string
	OutputPath      string
	Content         string
	Error           error
}

// resolveTemplateName resolves Go template expressions (e.g. "{{.secretName}}") using the given parameters
func resolveTemplateName(pattern string, params map[string]any) (string, error) {
	tmpl, err := template.New("secret").Parse(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid template expression %q: %w", pattern, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("resolving template expression %q: %w", pattern, err)
	}

	return buf.String(), nil
}

// processTemplateSecrets generates and encrypts secrets defined in a template's secrets[] spec.
// It returns the results and any written file paths.
func processTemplateSecrets(
	tmpl *templates.ClaimTemplate,
	renderParams map[string]any,
	secretValues map[string]string,
	config *RenderConfig,
) ([]SecretRenderResult, error) {
	if len(tmpl.Spec.Secrets) == 0 {
		return nil, nil
	}

	if config.SkipSecrets {
		fmt.Println("Skipping secrets (--skip-secrets)")
		return nil, nil
	}

	// Check SOPS prerequisites
	recipients, err := sops.CheckSOPSAvailable()
	if err != nil {
		return nil, fmt.Errorf("SOPS prerequisites: %w", err)
	}

	var results []SecretRenderResult

	for _, secretDef := range tmpl.Spec.Secrets {
		// Resolve name and namespace from template expressions
		secretName, err := resolveTemplateName(secretDef.Name, renderParams)
		if err != nil {
			results = append(results, SecretRenderResult{Error: err})
			continue
		}

		secretNamespace, err := resolveTemplateName(secretDef.Namespace, renderParams)
		if err != nil {
			results = append(results, SecretRenderResult{Error: err})
			continue
		}

		// Build stringData from secret values matching the secret's parameter definitions
		stringData := make(map[string]string)
		for _, param := range secretDef.Parameters {
			if val, ok := secretValues[param.Name]; ok {
				stringData[param.Name] = val
			} else if param.Required {
				results = append(results, SecretRenderResult{
					SecretName:      secretName,
					SecretNamespace: secretNamespace,
					Error:           fmt.Errorf("required secret parameter %q not provided", param.Name),
				})
				continue
			}
		}

		if len(stringData) == 0 {
			results = append(results, SecretRenderResult{
				SecretName:      secretName,
				SecretNamespace: secretNamespace,
				Error:           fmt.Errorf("no secret values provided for %s", secretName),
			})
			continue
		}

		// Generate Secret YAML
		fmt.Printf("Generating secret: %s/%s\n", secretNamespace, secretName)
		secretYAML, err := sops.GenerateSecretYAML(sops.SecretData{
			Name:       secretName,
			Namespace:  secretNamespace,
			StringData: stringData,
		})
		if err != nil {
			results = append(results, SecretRenderResult{
				SecretName:      secretName,
				SecretNamespace: secretNamespace,
				Error:           fmt.Errorf("generating secret YAML: %w", err),
			})
			continue
		}

		// Encrypt
		fmt.Println("Encrypting with SOPS...")
		encrypted, err := sops.Encrypt(secretYAML, recipients)
		if err != nil {
			results = append(results, SecretRenderResult{
				SecretName:      secretName,
				SecretNamespace: secretNamespace,
				Error:           fmt.Errorf("encrypting: %w", err),
			})
			continue
		}

		result := SecretRenderResult{
			SecretName:      secretName,
			SecretNamespace: secretNamespace,
			Content:         string(encrypted),
		}

		// Write file (unless dry-run)
		if !config.DryRun {
			filename := fmt.Sprintf("%s-secret.enc.yaml", secretName)
			if err := os.MkdirAll(config.OutputDir, 0755); err != nil {
				result.Error = fmt.Errorf("creating output directory: %w", err)
				results = append(results, result)
				continue
			}

			outputPath := filepath.Join(config.OutputDir, filename)
			if err := os.WriteFile(outputPath, encrypted, 0644); err != nil {
				result.Error = fmt.Errorf("writing encrypted file: %w", err)
				results = append(results, result)
				continue
			}

			result.OutputPath = outputPath
			fmt.Printf("Saved encrypted secret: %s\n", outputPath)
		} else {
			fmt.Printf("\nWould write encrypted secret: %s/%s-secret.enc.yaml\n", config.OutputDir, secretName)
			fmt.Println("[SOPS encrypted content omitted in dry-run]")
		}

		results = append(results, result)
	}

	return results, nil
}

// mergeSecretValues merges secret values from params file and inline --secret flags
func mergeSecretValues(fileSecrets map[string]string, inlineSecretsRaw []string) (map[string]string, error) {
	result := make(map[string]string)

	// Start with file secrets
	for k, v := range fileSecrets {
		result[k] = v
	}

	// Overlay inline secrets
	inlineSecrets, err := params.ParseInlineParams(inlineSecretsRaw)
	if err != nil {
		return nil, fmt.Errorf("parsing inline secrets: %w", err)
	}
	for k, v := range inlineSecrets {
		result[k] = fmt.Sprintf("%v", v)
	}

	return result, nil
}
