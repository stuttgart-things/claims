package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/stuttgart-things/claims/internal/conditions"
	"github.com/stuttgart-things/claims/internal/params"
	"github.com/stuttgart-things/claims/internal/templates"
)

// runNonInteractive runs the render command in non-interactive mode
func runNonInteractive(config *RenderConfig) error {
	// Validate required inputs
	if config.ParamsFile == "" && len(config.Templates) == 0 {
		return fmt.Errorf("non-interactive mode requires --params-file or --templates")
	}

	client := newAPIClient(config.APIUrl, config.Auth)

	// Parse parameter file if provided
	var templateParams []params.TemplateParams
	if config.ParamsFile != "" {
		pf, err := params.ParseFile(config.ParamsFile)
		if err != nil {
			return err
		}
		templateParams = pf.Templates
	}

	// Parse inline params
	inlineParams, err := params.ParseInlineParams(config.InlineParamsRaw)
	if err != nil {
		return err
	}

	// If templates specified via flag, use those
	if len(config.Templates) > 0 {
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
	} else {
		// Apply inline params to all templates from file
		for i := range templateParams {
			templateParams[i].Parameters = params.MergeParams(templateParams[i].Parameters, inlineParams)
		}
	}

	// Validate templates exist and build lookup map
	available, err := client.FetchTemplates()
	if err != nil {
		return fmt.Errorf("fetching templates: %w", err)
	}
	templateLookup := make(map[string]*templates.ClaimTemplate)
	for i, t := range available {
		templateLookup[t.Metadata.Name] = &available[i]
	}
	for _, tp := range templateParams {
		if templateLookup[tp.Name] == nil {
			return fmt.Errorf("template not found: %s", tp.Name)
		}
	}

	// Render all templates
	progress := config.progressOut()
	var results []RenderResult
	for i, tp := range templateParams {
		_, _ = fmt.Fprintf(progress, "Rendering %s...\n", tp.Name)

		// Values of parameters whose showWhen does not match are not sent
		// (the API would ignore them; claims#115).
		tp.Parameters = dropInactive(progress, templateLookup[tp.Name], tp.Parameters)
		templateParams[i].Parameters = tp.Parameters

		content, err := client.RenderTemplate(tp.Name, tp.Parameters)
		if err != nil {
			_, _ = fmt.Fprintf(progress, "  ERROR: %s\n", indentLines(err.Error(), "    "))
			results = append(results, RenderResult{
				TemplateName: tp.Name,
				Error:        err,
			})
			continue
		}

		resourceName := "output"
		if name, ok := tp.Parameters["name"]; ok {
			resourceName = fmt.Sprintf("%v", name)
		} else if tmpl := templateLookup[tp.Name]; tmpl != nil {
			for _, p := range tmpl.Spec.Parameters {
				if p.Name == "name" {
					if d, ok := p.Default.(string); ok && d != "" {
						resourceName = d
					}
					break
				}
			}
		}

		results = append(results, RenderResult{
			TemplateName: tp.Name,
			ResourceName: resourceName,
			Content:      content,
			Params:       tp.Parameters,
		})
		_, _ = fmt.Fprintf(progress, "  Rendered successfully\n")
	}

	// --stdout: the YAML to stdout, nothing written, no registry, no git.
	if config.Stdout {
		return writeStdout(os.Stdout, results, templateParams, templateLookup, config)
	}

	// The source-path layout: the order names its files; no --output-dir
	// pattern, no registry (claims#92).
	if config.Layout == LayoutSourcePath {
		for _, tp := range templateParams {
			if t := templateLookup[tp.Name]; t != nil && len(t.Spec.Secrets) > 0 {
				return fmt.Errorf("--layout %s does not handle secrets yet (template %s defines some)", LayoutSourcePath, tp.Name)
			}
		}
		orders, err := planLayout(results, templateParams, templateLookup)
		if err != nil {
			return err
		}
		return runSourcePathLayout(orders, config)
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
		FileMode:        config.FileMode,
	}

	if err := WriteResults(results, outputConfig); err != nil {
		return err
	}

	// Process secrets for templates that define them
	for _, tp := range templateParams {
		tmpl := templateLookup[tp.Name]
		if tmpl == nil || len(tmpl.Spec.Secrets) == 0 {
			continue
		}

		// Collect secret values from file and inline flags
		secretValues, err := mergeSecretValues(tp.Secrets, config.InlineSecretsRaw)
		if err != nil {
			return fmt.Errorf("collecting secret values: %w", err)
		}

		if len(secretValues) == 0 && !config.SkipSecrets {
			fmt.Printf("Template %s defines secrets but no secret values provided (use --secret or secrets: in params file)\n", tp.Name)
			continue
		}

		secretResults, err := processTemplateSecrets(tmpl, tp.Parameters, secretValues, config)
		if err != nil {
			return fmt.Errorf("processing secrets for %s: %w", tp.Name, err)
		}

		for _, sr := range secretResults {
			if sr.Error != nil {
				fmt.Printf("  Secret error (%s): %v\n", sr.SecretName, sr.Error)
				hasErrors = true
			} else if config.CombineSecrets && !config.DryRun {
				// Find the matching render result to get the output path
				for _, r := range results {
					if r.TemplateName == tp.Name && r.OutputPath != "" {
						if err := appendToFile(r.OutputPath, sr.Content); err != nil {
							fmt.Printf("  Failed to combine secret: %v\n", err)
							hasErrors = true
						} else {
							fmt.Printf("  Appended encrypted secret to: %s\n", r.OutputPath)
							// Remove the separate secret file
							if sr.OutputPath != "" && sr.OutputPath != r.OutputPath {
								_ = os.Remove(sr.OutputPath)
							}
						}
						break
					}
				}
			}
		}
	}

	// Update registry if output was written (and not dry-run)
	if !config.DryRun {
		updateRegistryForRender(results, config)
	}

	// Execute git operations if configured (and not dry-run)
	if !config.DryRun {
		if err := executeGitOperations(results, config); err != nil {
			return fmt.Errorf("git operations: %w", err)
		}
	}

	if hasErrors {
		return fmt.Errorf("some templates failed to render")
	}

	return nil
}

// writeStdout prints the successful renders as one YAML stream to out.
// Secrets are not generated (they would need files and SOPS); a note on
// stderr says so.
func writeStdout(out io.Writer, results []RenderResult, templateParams []params.TemplateParams, lookup map[string]*templates.ClaimTemplate, config *RenderConfig) error {
	if _, err := io.WriteString(out, renderedYAML(results)); err != nil {
		return err
	}
	if !config.SkipSecrets {
		for _, tp := range templateParams {
			if t := lookup[tp.Name]; t != nil && len(t.Spec.Secrets) > 0 {
				_, _ = fmt.Fprintf(os.Stderr, "Note: %s defines secrets; --stdout does not generate them\n", tp.Name)
			}
		}
	}
	for _, r := range results {
		if r.Error != nil {
			return fmt.Errorf("some templates failed to render")
		}
	}
	return nil
}

// dropInactive returns params without the values of tmpl's parameters
// whose showWhen does not match, and notes each dropped one on progress.
func dropInactive(progress io.Writer, tmpl *templates.ClaimTemplate, params map[string]any) map[string]any {
	if tmpl == nil || !conditions.HasConditions(tmpl.Spec.Parameters) {
		return params
	}
	out, dropped := conditions.Drop(tmpl.Spec.Parameters, params)
	for _, name := range dropped {
		for _, p := range tmpl.Spec.Parameters {
			if p.Name == name {
				_, _ = fmt.Fprintf(progress, "  Not sending %s: only applies when %s\n", name, conditions.ShowWhenText(p))
			}
		}
	}
	return out
}

// indentLines indents every line of s after the first by prefix.
func indentLines(s, prefix string) string {
	return strings.ReplaceAll(s, "\n", "\n"+prefix)
}
