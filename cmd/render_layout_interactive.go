package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/stuttgart-things/claims/internal/gitops"
	"github.com/stuttgart-things/claims/internal/layout"
	"github.com/stuttgart-things/claims/internal/params"
	"github.com/stuttgart-things/claims/internal/templates"
)

// interactiveLayout is the source-path layout of an interactive render: the
// orders and the repository they go into.
type interactiveLayout struct {
	Orders   []layoutOrder
	RepoRoot string
}

// planInteractiveLayout returns the source-path layout for the results, or
// nil when it does not apply: a failed render, a document without
// source-path/target-path, a template with secrets (the layout does not
// handle them yet), or a cwd outside a git repository.
func planInteractiveLayout(results []RenderResult, lookup map[string]*templates.ClaimTemplate, config *RenderConfig) *interactiveLayout {
	root, err := findRepoRoot(".")
	if err != nil {
		return nil
	}
	return planInteractiveLayoutIn(root, results, lookup, config)
}

func planInteractiveLayoutIn(root string, results []RenderResult, lookup map[string]*templates.ClaimTemplate, config *RenderConfig) *interactiveLayout {
	if len(results) == 0 {
		return nil
	}
	var tps []params.TemplateParams
	for _, r := range results {
		if r.Error != nil || sourcePathOf(r.Content) == "" {
			return nil
		}
		if t := lookup[r.TemplateName]; t != nil && len(t.Spec.Secrets) > 0 && !config.SkipSecrets {
			return nil
		}
		tps = append(tps, params.TemplateParams{Name: r.TemplateName, Parameters: r.Params})
	}
	orders, err := planLayout(results, tps, lookup)
	if err != nil {
		return nil
	}
	return &interactiveLayout{Orders: orders, RepoRoot: root}
}

// layoutSummary lists the files, kustomization entries, branch and title.
func layoutSummary(l *interactiveLayout, currentBranch string) string {
	conv := conventionFor(l.Orders)
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Repository: %s\n", l.RepoRoot)
	for _, f := range allFiles(l.Orders) {
		_, _ = fmt.Fprintf(&b, "Write:      %s\n", f.Path)
	}
	if changes, err := layout.Kustomizations(l.RepoRoot, allFiles(l.Orders)); err == nil {
		for _, c := range changes {
			_, _ = fmt.Fprintf(&b, "Append to:  %s (%s)\n", c.Path, strings.Join(c.Lines, ", "))
		}
	}
	_, _ = fmt.Fprintf(&b, "Branch:     %s (from %s)\n", conv.Branch, currentBranch)
	_, _ = fmt.Fprintf(&b, "PR title:   %s\n", conv.Title)
	b.WriteString("No claims/registry.yaml entry: the order's file is the record.")
	return b.String()
}

// runInteractiveLayout shows the plan and writes it: with a branch, commit,
// push and PR (the default), or the files only. done is false when the user
// goes back to the destination choice.
func runInteractiveLayout(l *interactiveLayout, config *RenderConfig) (done bool, err error) {
	currentBranch := "?"
	if g, err := gitops.New(l.RepoRoot, "", ""); err == nil {
		if b, err := g.GetCurrentBranch(); err == nil {
			currentBranch = b
		}
	}

	printSection(progressStyle, "━━━ Source-path layout ━━━")
	fmt.Println(layoutSummary(l, currentBranch))
	if currentBranch != "main" {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Note: the branch starts from %s, not main.", currentBranch)))
	}
	fmt.Println()

	choice := "pr"
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Write the order?").
			Options(
				huh.NewOption("Write, commit, push & open the PR", "pr"),
				huh.NewOption("Write the files only (no git)", "files"),
				huh.NewOption("Back", "back"),
			).
			Value(&choice),
	))
	if err := form.Run(); err != nil {
		return false, err
	}

	cfg := *config
	cfg.OutputDir = l.RepoRoot
	cfg.DryRun = false
	cfg.GitConfig, cfg.PRConfig = nil, nil
	switch choice {
	case "back":
		return false, nil
	case "pr":
		cfg.GitConfig = &GitConfig{Commit: true, Push: true, CreateBranch: true, Remote: "origin"}
		cfg.PRConfig = &PRConfig{Create: true, BaseBranch: "main"}
	}

	fmt.Println()
	return true, runSourcePathLayout(l.Orders, &cfg)
}
