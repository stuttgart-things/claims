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
go run . render --non-interactive -a http://localhost:8080 -f tests/params.yaml -o output

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
| `<cmd>_pr.go` | PR creation via the GitHub API (`gitops.CreatePR`) |

Commands auto-detect TTY to choose interactive vs non-interactive mode using `go-isatty`.

### Internal Packages

- **`internal/templates`** - HTTP client for claim-machinery-api (`GET /api/v1/claim-templates`, `POST .../order`). Uses `NewClientWithHTTPClient()` for test injection.
- **`internal/apiauth`** - Optional bearer-token auth for claim-machinery-api (off by default): `CLAIM_API_TOKEN`/`--api-token`, or OAuth2 client credentials `CLAIM_API_OIDC_*`/`--api-oidc-*` (token cached until expiry). Wired in via `newAPIClient()` in `cmd/apiauth.go` -> `templates.NewClientWithAuth()`; 401/403 errors carry `apiauth.StatusHint`.
- **`internal/gitops`** - Git operations via go-git: clone, branch, commit, push. PR creation uses the GitHub API (go-github, `internal/ghclient`) in the repository of the checkout's `origin`. Credential resolution: flags > `GIT_USER/GIT_TOKEN` > `GITHUB_USER/GITHUB_TOKEN` > `gh auth token`; the user is optional.
- **`internal/conditions`** - Parameter conditions `showWhen`/`enumWhen` (claims#115), a copy of claim-machinery-api's `internal/claimtemplate/conditions.go` semantics (keep both in sync): `Active`, `AllowedValues`, `Drop`, `Check`, `CheckValue`. `cmd/render_form.go` builds the interactive parameter form from it (a group per `showWhen`, hidden via `WithHideFunc`; `enumSelect` wraps huh's select for `enumWhen`; `fitToTerminal` sizes the groups before `Run`); non-interactive render drops inactive params. API errors are `*templates.APIError` (`Fields` of a 400).
- **`internal/params`** - Parameter file parsing (YAML/JSON). Supports single `template:` and multi `templates:` format. Inline params parsed as `key=value`.
- **`internal/registry`** - CRUD for `claims/registry.yaml` (tracks rendered claims as entries with metadata).
- **`internal/xrdelete`** - Finds, lists and plans the removal of XRs in `clusters/*/*/*-xrs/`. The `…FS` variants work on any `fs.FS`; the repoRoot variants wrap `os.DirFS`.
- **`internal/ghfs`** - Read-only `fs.FS` over one ref of a GitHub repository (go-github: 1 tree call, blobs on demand plus `Prefetch`). `cmd/fleet.go` uses it when there is no local checkout of the fleet repository (claims#107). Tests stub `openRemoteFleet` in `TestMain` (`cmd/fleet_test.go`).
- **`internal/ghclient`** - The go-github client for every GitHub call (claims#108; the `gh` CLI is optional): `Token(flag)` = flag, `GIT_TOKEN`, `GITHUB_TOKEN`, then `gh auth token` (`GHAuthToken`, a var); `New` honours `GITHUB_API_URL`; `Describe` turns 401/403/404/422 into hints. Tests point `GITHUB_API_URL` at an `httptest` server (`cmd/github_fake_test.go`); `TestMain` points it at a dead address and stubs `GHAuthToken`.
- **`cmd/merge.go`** - `claims merge` and `--merge` on render/change/delete (claims#113): resolves the PR (number, URL, or the branches `vm-`/`change-claim/`/`delete-claim/`/`claim-`), waits for the checks (required ones, else all), squash-merges with the head SHA pinned and deletes the branch. Guard rails: only claims branches, same repo, into the default branch; merges only on `mergeable_state` clean/unstable/has_hooks, never via an admin bypass. Every PR-creation site calls `mergeCreatedPR` when `PRConfig.Merge` / `ChangeConfig.Merge` is set. Tests: `cmd/merge_test.go` (`fakeMergeAPI`; `mergePoll`, `mergeCheckGrace`, `stdinIsTerminal`, `confirmMerge` are vars).
- **`internal/endpoint`** - Named claim-machinery-api endpoints in `~/.config/claims/config.yaml` (`$CLAIMS_CONFIG_FILE`): url, issuer, client-id, ca-file, current (claims#116). `cmd/endpoint.go` resolves the API: `-a` > `$CLAIM_API_URL` > `--endpoint`/`$CLAIM_ENDPOINT` > current; no localhost default (interactive: picker, `pickEndpoint` var; else `errNoEndpoint`).
- **`internal/kustomize`** - Read/write `kustomization.yaml`, idempotent resource add/remove.
- **`internal/sops`** - SOPS encryption via age. Requires `sops` binary and `SOPS_AGE_RECIPIENTS` env var.

### Preflight (claims#114)

`cmd/preflight.go`: `render`/`encrypt`/`delete` collect a `preflightReport` (API endpoint, `GET /api/v1/claim-templates`, auth, git repo/branch/token/`git ls-remote` via the `probeRemote` var, GitHub push permission) before writing anything and fail with every problem + fix; `--skip-preflight` skips it, `claims doctor` (`cmd/doctor.go`) runs it read-only. Push/PR failures after the commit are wrapped by `pushFailed`/`prFailed` (`cmd/push_failure.go`) with retry/undo commands instead of a rollback. SSH remotes are pushed/cloned over HTTPS with the token (`gitops.HTTPSURL`); the token alone suffices (`gitops.TokenUser`), `ResolveCredentials` falls back to `gh auth token`.

### GitOps Workflow

render/delete/encrypt commands optionally perform a full git workflow: resolve creds -> clone or open local repo -> create branch -> write files -> update registry -> stage -> commit -> push -> create PR.

### Lipgloss Styles

Shared terminal styles and helpers: `cmd/ui.go` (printSection, styleLines, exitOn), `cmd/render_interactive.go` (successStyle, errorStyle, progressStyle, yamlStyle) and `cmd/render_review.go` (reviewHeaderStyle, resourceHeaderStyle, previewStyle). Import `lipgloss/v2`.

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

Binary version info is injected via ldflags into `cmd.version`, `cmd.buildDate`, `cmd.commit` (the only targets). See `task build-local`, the `build` task and `.goreleaser.yaml`. Empty values fall back to `debug.ReadBuildInfo()` (`internal/buildinfo`); unknown fields are left out of the banner and `claims version`.

## Terminal Output

- The banner shows only for the root help, only on a TTY, never with `NO_COLOR`.
- Never pass newlines into a lipgloss `Render` (it pads lines with spaces): use `printSection` / `styleLines` in `cmd/ui.go`.
- End commands with `exitOn(err)`: a user abort (`huh.ErrUserAborted`) exits 130 with a short note, errors exit 1 on stderr.

## Branch Naming

`feat/*`, `fix/*`, `docs/*` - CI triggers on push/PR to `main`, `feat/*`, `fix/*`.
