# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
# Run all unit tests
go test ./...

# Run a single test by name
go test ./cmd/ -run TestRenderNonInteractive -v

# Run integration tests (requires running claim-machinery-api)
CLAIM_API_URL=http://localhost:8080 go test -tags=integration ./tests/integration/...

# Build (uses Dagger, outputs to /tmp/go/build/)
task build

# Run render locally with test params
task render
go run . render --non-interactive -f tests/params.yaml -o output

# Start the claim-machinery-api locally via Dagger
task run-claim-api

# Tidy modules
go mod tidy
```

Note: `task build` and `task lint` delegate to remote Taskfiles via Dagger. For quick local builds, use `go build -o ./bin/claims .` directly.

## Architecture

**Entry point**: `main.go` calls `cmd.Execute()`. All CLI commands live in `cmd/` (single package `cmd`).

### Command Pattern

Each command (render, delete, encrypt) follows a consistent multi-file split:

| Suffix | Role |
|---|---|
| `<cmd>.go` | Cobra command definition, flags, TTY detection, dispatcher |
| `<cmd>_types.go` | Config/result structs |
| `<cmd>_interactive.go` | Terminal UI using charmbracelet/huh forms |
| `<cmd>_noninteractive.go` | Flag/params-file driven execution |
| `<cmd>_output.go` | File writing, dry-run display |
| `<cmd>_review.go` | YAML preview and action selection |
| `<cmd>_git.go` | Git operations (clone/branch/commit/push) |
| `<cmd>_pr.go` | PR creation via `gh` CLI |

Commands auto-detect TTY to choose interactive vs non-interactive mode using `go-isatty`.

### Internal Packages

- **`internal/templates`** - HTTP client for claim-machinery-api (`GET /api/v1/claim-templates`, `POST .../order`). Uses `NewClientWithHTTPClient()` for test injection.
- **`internal/gitops`** - Git operations via go-git: clone, branch, commit, push. PR creation shells out to `gh` CLI. Credential resolution: flags > `GIT_USER/GIT_TOKEN` > `GITHUB_USER/GITHUB_TOKEN`.
- **`internal/params`** - Parameter file parsing (YAML/JSON). Supports single `template:` and multi `templates:` format. Inline params parsed as `key=value`.
- **`internal/registry`** - CRUD for `claims/registry.yaml` (tracks rendered claims as entries with metadata).
- **`internal/kustomize`** - Read/write `kustomization.yaml`, idempotent resource add/remove.
- **`internal/sops`** - SOPS encryption via age. Requires `sops` binary and `SOPS_AGE_RECIPIENTS` env var.

### GitOps Workflow

render/delete/encrypt commands optionally perform a full git workflow: resolve creds -> clone or open local repo -> create branch -> write files -> update registry -> stage -> commit -> push -> create PR.

### Lipgloss Styles

Shared terminal styles are defined in `cmd/render_interactive.go` (successStyle, errorStyle, progressStyle, yamlStyle) and `cmd/render_review.go` (reviewHeaderStyle, resourceHeaderStyle, previewStyle). Import `lipgloss/v2`.

## Test Conventions

- `cmd/` tests use `package cmd` (white-box)
- `internal/` tests use external test packages (e.g., `package gitops_test`)
- Table-driven subtests with `t.Run()`
- `t.TempDir()` for filesystem tests
- `httptest.NewServer()` for HTTP client tests
- `t.Setenv()` for environment variable isolation
- SOPS tests skip when binary/env not available
- Integration tests use `//go:build integration` build tag

## Version Injection

Binary version info is injected via ldflags into `cmd.version`, `cmd.buildDate`, `cmd.commit`. See `Taskfile.yaml` build task and `.goreleaser.yaml`.

## Branch Naming

`feat/*`, `fix/*`, `docs/*` - CI triggers on push/PR to `main`, `feat/*`, `fix/*`.
