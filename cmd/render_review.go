package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// ReviewAction represents the user's choice in the review step
type ReviewAction string

const (
	ReviewActionContinue ReviewAction = "continue"
	ReviewActionEdit     ReviewAction = "edit"
	ReviewActionCancel   ReviewAction = "cancel"
)

// Styles for review UI
var (
	reviewHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("205")).
				MarginTop(1).
				MarginBottom(1)

	resourceHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("86"))

	previewStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
)

// ReviewResults displays rendered results and allows user to continue, edit, or cancel
// Returns the chosen action, the index of the template to edit (if action is edit), and any error
func ReviewResults(results []RenderResult) (ReviewAction, int, error) {
	fmt.Println(reviewHeaderStyle.Render("━━━ Review Rendered Resources ━━━"))

	// Count successful renders
	successCount := 0
	for _, r := range results {
		if r.Error == nil {
			successCount++
		}
	}

	// Display each rendered resource
	for _, r := range results {
		if r.Error != nil {
			fmt.Printf("\n%s %s (ERROR: %v)\n",
				errorStyle.Render("✗"),
				r.TemplateName,
				r.Error,
			)
			continue
		}

		header := fmt.Sprintf("📄 %s (%s):", r.TemplateName, r.ResourceName)
		fmt.Println(resourceHeaderStyle.Render(header))

		// Truncate long YAML for preview
		preview := truncateYAML(r.Content, 15)
		fmt.Println(previewStyle.Render(preview))
		fmt.Println()
	}

	// If no successful renders, only allow cancel
	if successCount == 0 {
		fmt.Println(errorStyle.Render("No successful renders to save."))
		return ReviewActionCancel, 0, nil
	}

	// Action selection
	var action string

	actionForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("What would you like to do?").
				Options(
					huh.NewOption("Continue to save", "continue"),
					huh.NewOption("Edit a template's parameters", "edit"),
					huh.NewOption("Cancel", "cancel"),
				).
				Value(&action),
		),
	)

	if err := actionForm.Run(); err != nil {
		return ReviewActionCancel, 0, err
	}

	if action == "edit" {
		// Let user select which template to edit
		editIndex, err := selectTemplateToEdit(results)
		if err != nil {
			return ReviewActionCancel, 0, err
		}
		return ReviewActionEdit, editIndex, nil
	}

	return ReviewAction(action), 0, nil
}

// selectTemplateToEdit displays a form to select which template to edit
func selectTemplateToEdit(results []RenderResult) (int, error) {
	// Build options only from successful renders
	type indexedOption struct {
		index int
		label string
	}
	var validOptions []indexedOption

	for i, r := range results {
		if r.Error == nil {
			label := fmt.Sprintf("%s (%s)", r.TemplateName, r.ResourceName)
			validOptions = append(validOptions, indexedOption{index: i, label: label})
		}
	}

	if len(validOptions) == 0 {
		return 0, fmt.Errorf("no templates available to edit")
	}

	// If only one option, return it directly
	if len(validOptions) == 1 {
		return validOptions[0].index, nil
	}

	// Build huh options
	options := make([]huh.Option[int], len(validOptions))
	for i, opt := range validOptions {
		options[i] = huh.NewOption(opt.label, opt.index)
	}

	var selected int
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title("Which template do you want to edit?").
				Options(options...).
				Value(&selected),
		),
	)

	if err := form.Run(); err != nil {
		return 0, err
	}

	return selected, nil
}

// truncateYAML truncates YAML content to a maximum number of lines
func truncateYAML(content string, maxLines int) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) <= maxLines {
		return strings.TrimSpace(content)
	}

	truncated := strings.Join(lines[:maxLines], "\n")
	return truncated + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}

// ShowFullPreview displays complete YAML for a single result
func ShowFullPreview(result RenderResult) {
	fmt.Println(reviewHeaderStyle.Render(
		fmt.Sprintf("━━━ Full Preview: %s ━━━", result.TemplateName),
	))
	fmt.Println(yamlStyle.Render(result.Content))
}
