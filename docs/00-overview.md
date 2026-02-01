# Claims CLI: Multi-Template Rendering & GitOps - Overview

## Goal
Extend `claims render` to support batch template rendering with both interactive (huh forms) and non-interactive (flags) modes, plus git commit/push/PR for GitOps workflows.

---

## New CLI Flags

```bash
claims render [flags]

# Mode Control
  -i, --interactive          Force interactive mode (default when TTY)
      --non-interactive      Force non-interactive mode (default when no TTY)

# Template Selection
  -t, --templates strings    Templates to render (comma-separated or repeated)

# Parameter Input
  -f, --params-file string   YAML/JSON file with parameters
  -p, --param strings        Inline params (key=value, repeatable)

# Output Control
  -o, --output-dir string    Output directory (default: /tmp)
      --dry-run              Print output without writing files
      --single-file          Combine all rendered resources into one file
      --filename-pattern     Pattern for filenames (default: {{.template}}-{{.name}}.yaml)

# GitOps
  -g, --git-commit           Commit rendered files
      --git-push             Push to remote (implies --git-commit)
      --git-message string   Commit message
      --git-branch string    Target branch (default: current)
      --git-create-branch    Create new branch before committing
      --git-remote string    Remote name (default: origin)
      --git-repo string      Clone this repo first (for stateless CI/CD)
      --git-user string      Git username (default: $GIT_USER)
      --git-token string     Git token (default: $GIT_TOKEN or $GITHUB_TOKEN)

# Pull Request
      --create-pr            Create a pull request after push
      --pr-title string      PR title (default: auto-generated from templates)
      --pr-description string PR description
      --pr-labels strings    PR labels (comma-separated)
      --pr-base string       Base branch for PR (default: main)
```

---

## File Structure

```
cmd/
├── render.go                # Entry point, flags, mode routing
├── render_interactive.go    # Interactive huh forms logic
├── render_noninteractive.go # Non-interactive flag-based logic
├── render_types.go          # Shared types (RenderConfig, RenderResult)
├── render_output.go         # File output & organization logic
├── render_git.go            # Git operations wrapper
├── render_pr.go             # Pull request creation (gh CLI wrapper)
├── render_review.go         # Review/preview rendered resources

internal/
├── templates/
│   ├── types.go             # ClaimTemplate, Parameter (moved from render.go)
│   └── client.go            # API client (fetchTemplates, renderTemplate)
├── params/
│   └── file.go              # Parameter file parsing (YAML/JSON)
└── gitops/
    ├── operations.go        # Git commit/push using go-git
    ├── branch.go            # Branch creation
    └── pr.go                # PR creation via gh CLI or GitHub API
```

---

## Implementation Order

1. [01-foundation.md](01-foundation.md) - Refactoring & type extraction
2. [02-output.md](02-output.md) - Output control & file organization
3. [03-multiselect.md](03-multiselect.md) - Multi-template support
4. [04-review.md](04-review.md) - Review step
5. [05-noninteractive.md](05-noninteractive.md) - Non-interactive mode
6. [06-gitops.md](06-gitops.md) - Git integration
7. [07-pullrequest.md](07-pullrequest.md) - PR creation
8. [08-polish.md](08-polish.md) - Testing & documentation
