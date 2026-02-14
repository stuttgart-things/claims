# claims

Terminal-based CLI for rendering claims via the claim-machinery API.

## Overview

Claims is an interactive CLI tool that connects to the claim-machinery API, fetches available claim templates, and provides a terminal UI for selecting templates, filling in parameters, and rendering claims.

## Getting Started

### Prerequisites

- Go 1.25.6+
- [Task](https://taskfile.dev/) (optional)
- claim-machinery API running (default: `http://localhost:8080`)

### Installation

```bash
git clone https://github.com/stuttgart-things/claims.git
cd claims
go mod tidy
go build -o claims .
```

### Running

```bash
# Using go directly
go run . render

# Using the built binary
./claims render

# Using Task
task run
```

## Commands

| Command | Description |
|---------|-------------|
| `claims render` | Interactively render a claim template via API |
| `claims encrypt` | Create a SOPS-encrypted Kubernetes Secret via Git PR |
| `claims delete` | Delete a claim via Git PR |
| `claims list` | List claims from the registry |
| `claims version` | Print version information |

See the [README](https://github.com/stuttgart-things/claims#readme) for full flag documentation and examples.

### encrypt

Create SOPS-encrypted Kubernetes Secrets. See [09-encrypt.md](09-encrypt.md) for full details.

```bash
# Interactive
claims encrypt

# Non-interactive
claims encrypt --non-interactive \
  --template my-secret-template \
  --name app-secrets \
  --namespace production \
  --param db_password=secret123
```

**Prerequisites:** `sops` CLI + `SOPS_AGE_RECIPIENTS` env var.

## Configuration

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `CLAIM_API_URL` | API base URL | `http://localhost:8080` |
| `SOPS_AGE_RECIPIENTS` | age public key for SOPS encryption | - |
| `GIT_USER` | Git username for push operations | - |
| `GIT_TOKEN` | Git token/password for push operations | - |
| `GITHUB_TOKEN` | GitHub token (fallback for `GIT_TOKEN`) | - |

## Development

### Available Tasks

| Task | Description |
|------|-------------|
| `task build` | Build the binary |
| `task run` | Run the application |
| `task test` | Run tests |
| `task test-coverage` | Run tests with coverage |
| `task lint` | Run linter |
| `task fmt` | Format code |
| `task tidy` | Tidy go modules |

### Project Structure

```
.
├── main.go                       # Application entry point
├── cmd/
│   ├── root.go                   # Root command setup
│   ├── render.go                 # Render command and flags
│   ├── render_interactive.go     # Interactive render flow
│   ├── render_noninteractive.go  # Non-interactive render
│   ├── render_git.go             # Git operations for render
│   ├── encrypt.go                # Encrypt command and flags
│   ├── encrypt_interactive.go    # Interactive encrypt flow (SOPS)
│   ├── encrypt_noninteractive.go # Non-interactive encrypt
│   ├── encrypt_git.go            # Git operations for encrypt
│   ├── delete.go                 # Delete command and flags
│   ├── list.go                   # List command
│   ├── version.go                # Version command
│   └── logo.go                   # ASCII logo rendering
├── internal/
│   ├── sops/                     # SOPS encryption + K8s Secret generation
│   ├── templates/                # API client for claim-machinery
│   ├── gitops/                   # Git operations (clone, commit, push, PR)
│   ├── params/                   # Parameter file parsing
│   ├── registry/                 # Registry.yaml CRUD
│   └── kustomize/                # Kustomization.yaml operations
├── go.mod                        # Go module definition
├── Taskfile.yaml                 # Task automation
├── docs/                         # Documentation (TechDocs)
└── catalog-info.yaml             # Backstage component
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feat/amazing-feature`)
5. Open a Pull Request
