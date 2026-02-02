package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// OutputConfig holds configuration for file output
type OutputConfig struct {
	Directory       string
	FilenamePattern string
	SingleFile      bool
	DryRun          bool
}

// FileInfo holds information used for filename generation
type FileInfo struct {
	TemplateName string
	ResourceName string
}

// GenerateFilename creates a filename from pattern and file info
func GenerateFilename(pattern string, info FileInfo) (string, error) {
	tmpl, err := template.New("filename").Parse(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid filename pattern: %w", err)
	}

	data := map[string]string{
		"template": info.TemplateName,
		"name":     info.ResourceName,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing filename template: %w", err)
	}

	return buf.String(), nil
}

// WriteResults writes render results to files based on the output configuration
func WriteResults(results []RenderResult, config OutputConfig) error {
	if config.DryRun {
		return printDryRun(results, config)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(config.Directory, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	if config.SingleFile {
		return writeSingleFile(results, config)
	}
	return writeSeparateFiles(results, config)
}

// writeSingleFile combines all results into a single YAML file separated by ---
func writeSingleFile(results []RenderResult, config OutputConfig) error {
	var combined strings.Builder

	for i, r := range results {
		if r.Error != nil {
			continue // Skip failed renders
		}
		if combined.Len() > 0 {
			combined.WriteString("\n---\n")
		}
		// Ensure content doesn't have leading/trailing newlines for cleaner output
		combined.WriteString(strings.TrimSpace(r.Content))
		if i < len(results)-1 {
			combined.WriteString("\n")
		}
	}

	// Use first template name for combined file
	filename := "combined-claims.yaml"
	if len(results) > 0 && results[0].TemplateName != "" {
		filename = fmt.Sprintf("%s-combined.yaml", results[0].TemplateName)
	}

	path := filepath.Join(config.Directory, filename)
	if err := os.WriteFile(path, []byte(combined.String()), 0644); err != nil {
		return fmt.Errorf("writing combined file: %w", err)
	}

	fmt.Printf("Saved combined file: %s\n", path)
	return nil
}

// writeSeparateFiles writes each result to its own file
func writeSeparateFiles(results []RenderResult, config OutputConfig) error {
	for i, r := range results {
		if r.Error != nil {
			continue // Skip failed renders
		}

		filename, err := GenerateFilename(config.FilenamePattern, FileInfo{
			TemplateName: r.TemplateName,
			ResourceName: r.ResourceName,
		})
		if err != nil {
			return err
		}

		path := filepath.Join(config.Directory, filename)
		if err := os.WriteFile(path, []byte(r.Content), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}

		// Update the result with the output path
		results[i].OutputPath = path
		fmt.Printf("Saved: %s\n", path)
	}
	return nil
}

// printDryRun displays what would be written without actually writing files
func printDryRun(results []RenderResult, config OutputConfig) error {
	fmt.Println("\n=== DRY RUN - No files written ===")

	if config.SingleFile {
		filename := "combined-claims.yaml"
		if len(results) > 0 && results[0].TemplateName != "" {
			filename = fmt.Sprintf("%s-combined.yaml", results[0].TemplateName)
		}
		path := filepath.Join(config.Directory, filename)
		fmt.Printf("Would write combined file: %s\n\n", path)

		for i, r := range results {
			if r.Error != nil {
				fmt.Printf("# Skipping failed render: %s/%s\n", r.TemplateName, r.ResourceName)
				continue
			}
			if i > 0 {
				fmt.Println("---")
			}
			fmt.Println(yamlStyle.Render(strings.TrimSpace(r.Content)))
		}
	} else {
		for _, r := range results {
			if r.Error != nil {
				fmt.Printf("# Skipping failed render: %s/%s - %v\n", r.TemplateName, r.ResourceName, r.Error)
				continue
			}

			filename, err := GenerateFilename(config.FilenamePattern, FileInfo{
				TemplateName: r.TemplateName,
				ResourceName: r.ResourceName,
			})
			if err != nil {
				filename = fmt.Sprintf("%s-%s.yaml", r.TemplateName, r.ResourceName)
			}
			path := filepath.Join(config.Directory, filename)

			fmt.Printf("Would write: %s\n", path)
			fmt.Println(yamlStyle.Render(strings.TrimSpace(r.Content)))
			fmt.Println()
		}
	}
	return nil
}

// WriteSingleResult writes a single render result using the output configuration
// This is a convenience function for backward compatibility with single-template rendering
func WriteSingleResult(templateName, resourceName, content string, config OutputConfig) error {
	result := RenderResult{
		TemplateName: templateName,
		ResourceName: resourceName,
		Content:      content,
	}
	return WriteResults([]RenderResult{result}, config)
}
