# Phase 7: Pull Request Support

## Goal
Add PR creation with labels and descriptions after pushing.

---

## Tasks

- [ ] Create `internal/gitops/pr.go` (gh CLI wrapper or GitHub API)
- [ ] Create `cmd/render_pr.go`
- [ ] Add `--create-pr`, `--pr-title`, `--pr-description`, `--pr-labels`, `--pr-base` flags
- [ ] Interactive form for PR options

---

## New Flags

```go
// cmd/render.go - add to init()
var (
    createPR      bool
    prTitle       string
    prDescription string
    prLabels      []string
    prBase        string
)

renderCmd.Flags().BoolVar(&createPR, "create-pr", false, "Create a pull request after push")
renderCmd.Flags().StringVar(&prTitle, "pr-title", "", "PR title")
renderCmd.Flags().StringVar(&prDescription, "pr-description", "", "PR description")
renderCmd.Flags().StringSliceVar(&prLabels, "pr-labels", nil, "PR labels (comma-separated)")
renderCmd.Flags().StringVar(&prBase, "pr-base", "main", "Base branch for PR")
```

---

## PR Creation via gh CLI

### `internal/gitops/pr.go`

```go
package gitops

import (
    "bytes"
    "fmt"
    "os/exec"
    "strings"
)

type PRConfig struct {
    Title       string
    Description string
    Labels      []string
    BaseBranch  string
    HeadBranch  string
}

type PRResult struct {
    Number int
    URL    string
}

// CreatePR creates a pull request using gh CLI
func CreatePR(config PRConfig, repoPath string) (*PRResult, error) {
    // Check if gh is available
    if _, err := exec.LookPath("gh"); err != nil {
        return nil, fmt.Errorf("gh CLI not found: install from https://cli.github.com")
    }

    args := []string{"pr", "create",
        "--title", config.Title,
        "--body", config.Description,
        "--base", config.BaseBranch,
    }

    // Add labels
    for _, label := range config.Labels {
        args = append(args, "--label", label)
    }

    // Add head branch if specified
    if config.HeadBranch != "" {
        args = append(args, "--head", config.HeadBranch)
    }

    cmd := exec.Command("gh", args...)
    cmd.Dir = repoPath

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return nil, fmt.Errorf("gh pr create failed: %s", stderr.String())
    }

    // Parse PR URL from output
    prURL := strings.TrimSpace(stdout.String())

    return &PRResult{
        URL: prURL,
    }, nil
}

// AddLabelsToPR adds labels to an existing PR
func AddLabelsToPR(prNumber int, labels []string, repoPath string) error {
    if len(labels) == 0 {
        return nil
    }

    args := []string{"pr", "edit", fmt.Sprintf("%d", prNumber)}
    for _, label := range labels {
        args = append(args, "--add-label", label)
    }

    cmd := exec.Command("gh", args...)
    cmd.Dir = repoPath

    var stderr bytes.Buffer
    cmd.Stderr = &stderr

    if err := cmd.Run(); err != nil {
        return fmt.Errorf("adding labels failed: %s", stderr.String())
    }

    return nil
}

// CheckGHAuth verifies gh CLI is authenticated
func CheckGHAuth() error {
    cmd := exec.Command("gh", "auth", "status")
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("gh CLI not authenticated: run 'gh auth login'")
    }
    return nil
}
```

---

## PR Integration

### `cmd/render_pr.go`

```go
package cmd

import (
    "fmt"
    "strings"

    "github.com/stuttgart-things/claims/internal/gitops"
)

func executePRCreation(results []RenderResult, config *RenderConfig, repoPath string) error {
    if config.PRConfig == nil || !config.PRConfig.Create {
        return nil
    }

    // Check gh authentication
    if err := gitops.CheckGHAuth(); err != nil {
        return err
    }

    // Generate title if not provided
    title := config.PRConfig.Title
    if title == "" {
        var names []string
        for _, r := range results {
            if r.Error == nil {
                names = append(names, r.TemplateName)
            }
        }
        title = fmt.Sprintf("Rendered claims: %s", strings.Join(names, ", "))
    }

    // Generate description if not provided
    description := config.PRConfig.Description
    if description == "" {
        description = generatePRDescription(results, config)
    }

    prConfig := gitops.PRConfig{
        Title:       title,
        Description: description,
        Labels:      config.PRConfig.Labels,
        BaseBranch:  config.PRConfig.BaseBranch,
        HeadBranch:  config.GitConfig.Branch,
    }

    fmt.Println("Creating pull request...")
    pr, err := gitops.CreatePR(prConfig, repoPath)
    if err != nil {
        return err
    }

    fmt.Printf("✓ Created PR: %s\n", pr.URL)
    return nil
}

func generatePRDescription(results []RenderResult, config *RenderConfig) string {
    var sb strings.Builder

    sb.WriteString("## Summary\n\n")
    sb.WriteString("Rendered claim templates:\n\n")

    for _, r := range results {
        if r.Error == nil {
            sb.WriteString(fmt.Sprintf("- **%s** → `%s`\n", r.TemplateName, r.OutputPath))
        }
    }

    sb.WriteString("\n## Files Changed\n\n")
    for _, r := range results {
        if r.Error == nil && r.OutputPath != "" {
            sb.WriteString(fmt.Sprintf("- `%s`\n", r.OutputPath))
        }
    }

    sb.WriteString("\n---\n")
    sb.WriteString("*Generated by claims CLI*\n")

    return sb.String()
}
```

---

## Interactive PR Form

```go
// cmd/render_interactive.go - extend git options form

func runPROptionsForm(gitAction string) (*PRConfig, error) {
    if gitAction != "pr" {
        return nil, nil
    }

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
                Value(&prTitle),

            huh.NewText().
                Title("PR Description").
                Description("Leave empty for auto-generated description").
                Value(&prDescription).
                CharLimit(1000),

            huh.NewInput().
                Title("Labels").
                Description("Comma-separated labels (e.g., infrastructure,automated)").
                Value(&prLabels),

            huh.NewInput().
                Title("Base branch").
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
            labels = append(labels, strings.TrimSpace(l))
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
```

---

## Complete Git+PR Flow

```go
// cmd/render_git.go - update executeGitOperations

func executeGitOperations(results []RenderResult, config *RenderConfig) error {
    // ... existing git operations (commit, push)

    // After successful push, create PR if requested
    if config.GitConfig.Push && config.PRConfig != nil && config.PRConfig.Create {
        if err := executePRCreation(results, config, repoPath); err != nil {
            return fmt.Errorf("PR creation failed: %w", err)
        }
    }

    return nil
}
```

---

## Verification

```bash
# Create PR with auto-generated title/description
claims render -o ./manifests \
  --git-create-branch --git-branch feature/update \
  --git-push \
  --create-pr

# Create PR with custom options
claims render -o ./manifests \
  --git-push \
  --create-pr \
  --pr-title "Infrastructure update" \
  --pr-description "Adding new VM resources" \
  --pr-labels "infrastructure,automated" \
  --pr-base main

# Interactive mode
claims render
# Select "Commit, Push & Create PR" in git options
# Fill in PR details via form
```

---

## Dependencies

Requires `gh` CLI installed and authenticated:
```bash
# Install
brew install gh  # macOS
# or: https://cli.github.com

# Authenticate
gh auth login
```

---

## Files to Create

| File | Action |
|------|--------|
| `internal/gitops/pr.go` | Create |
| `cmd/render_pr.go` | Create |
| `cmd/render.go` | Add PR flags |
| `cmd/render_interactive.go` | Add PR form |
| `cmd/render_git.go` | Integrate PR creation |
