package cmd

import (
	"fmt"

	"github.com/stuttgart-things/claims/internal/params"
	"github.com/stuttgart-things/claims/internal/templates"
)

// runNonInteractive runs the render command in non-interactive mode
func runNonInteractive(config *RenderConfig) error {
	// Validate required inputs
	if config.ParamsFile == "" && len(config.Templates) == 0 {
		return fmt.Errorf("non-interactive mode requires --params-file or --templates")
	}

	client := templates.NewClient(config.APIUrl)

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

	// Validate templates exist
	available, err := client.FetchTemplates()
	if err != nil {
		return fmt.Errorf("fetching templates: %w", err)
	}
	templateMap := make(map[string]bool)
	for _, t := range available {
		templateMap[t.Metadata.Name] = true
	}
	for _, tp := range templateParams {
		if !templateMap[tp.Name] {
			return fmt.Errorf("template not found: %s", tp.Name)
		}
	}

	// Render all templates
	var results []RenderResult
	for _, tp := range templateParams {
		fmt.Printf("Rendering %s...\n", tp.Name)

		content, err := client.RenderTemplate(tp.Name, tp.Parameters)
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
			results = append(results, RenderResult{
				TemplateName: tp.Name,
				Error:        err,
			})
			continue
		}

		resourceName := "output"
		if name, ok := tp.Parameters["name"]; ok {
			resourceName = fmt.Sprintf("%v", name)
		}

		results = append(results, RenderResult{
			TemplateName: tp.Name,
			ResourceName: resourceName,
			Content:      content,
			Params:       tp.Parameters,
		})
		fmt.Printf("  Rendered successfully\n")
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
	}

	if err := WriteResults(results, outputConfig); err != nil {
		return err
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
