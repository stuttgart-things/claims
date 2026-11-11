package cmd

import (
	"fmt"
	"os"
	"os/exec"
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
	ReviewActionFull     ReviewAction = "full"
)

// reviewPreviewLines is how many lines of each resource the review shows
// unless --full is set.
const reviewPreviewLines = 15

// Styles for review UI
var (
	// No margins: lipgloss renders them as lines of spaces. Spacing comes
	// from printSection (one blank line before every section header).
	reviewHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("205"))

	resourceHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("86"))

	previewStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
)

// ReviewResults displays rendered results and allows user to continue, edit, view
// the full YAML, or cancel. full shows every resource untruncated.
// Returns the chosen action, the index of the template to edit (if action is edit), and any error
func ReviewResults(results []RenderResult, full bool) (ReviewAction, int, error) {
	printSection(reviewHeaderStyle, "━━━ Review Rendered Resources ━━━")
	fmt.Println()

	// Count successful renders
	successCount := 0
	for _, r := range results {
		if r.Error == nil {
			successCount++
		}
	}

	// Display each rendered resource: header, box, one blank line
	truncated := false
	for _, r := range results {
		if r.Error != nil {
			// One line per field error of an API 400 (claims#115).
			fmt.Printf("%s %s: ERROR\n%s\n\n",
				errorStyle.Render("✗"),
				r.TemplateName,
				styleLines(errorStyle, "  "+indentLines(r.Error.Error(), "  ")),
			)
			continue
		}

		header := fmt.Sprintf("📄 %s (%s):", r.TemplateName, r.ResourceName)
		fmt.Println(resourceHeaderStyle.Render(header))

		// Truncate long YAML for preview
		preview := strings.TrimSpace(r.Content)
		if !full {
			preview = truncateYAML(r.Content, reviewPreviewLines)
			truncated = truncated || preview != strings.TrimSpace(r.Content)
		}
		fmt.Println(previewStyle.Render(preview))
		fmt.Println()
	}

	// Action selection; without a successful render only edit (the failed
	// ones, with the API's field errors next to their fields) or cancel.
	var action string

	var options []huh.Option[string]
	if successCount == 0 {
		fmt.Println(errorStyle.Render("No successful renders to save."))
	} else {
		options = append(options, huh.NewOption("Continue to save", "continue"))
	}
	if truncated {
		options = append(options, huh.NewOption("Show full YAML", string(ReviewActionFull)))
	}
	options = append(options,
		huh.NewOption("Edit a template's parameters", "edit"),
		huh.NewOption("Cancel", "cancel"),
	)

	actionForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("What would you like to do?").
				Options(options...).
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
	// Every template can be edited, a failed one to fix its parameters
	type indexedOption struct {
		index int
		label string
	}
	var validOptions []indexedOption

	for i, r := range results {
		label := fmt.Sprintf("%s (%s)", r.TemplateName, r.ResourceName)
		if r.Error != nil {
			label = r.TemplateName + " (failed)"
		}
		validOptions = append(validOptions, indexedOption{index: i, label: label})
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

// showFullResults shows the complete rendered YAML: through $PAGER (default
// "less -FRX") on a terminal, printed otherwise or when no pager is found.
func showFullResults(results []RenderResult) {
	content := renderedYAML(results)
	if isTTY(os.Stdout) {
		if err := runPager(content); err == nil {
			return
		}
	}
	printSection(reviewHeaderStyle, "━━━ Full YAML ━━━")
	fmt.Println()
	fmt.Print(content)
}

// runPager pipes content through $PAGER, or less -FRX.
func runPager(content string) error {
	pager := strings.Fields(os.Getenv("PAGER"))
	if len(pager) == 0 {
		pager = []string{"less", "-FRX"}
	}
	bin, err := exec.LookPath(pager[0])
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, pager[1:]...)
	cmd.Stdin = strings.NewReader(content)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ShowFullPreview displays complete YAML for a single result
func ShowFullPreview(result RenderResult) {
	printSection(reviewHeaderStyle, fmt.Sprintf("━━━ Full Preview: %s ━━━", result.TemplateName))
	fmt.Println(yamlStyle.Render(result.Content))
}
