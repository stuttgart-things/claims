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

### render

Interactively render a claim template via the API.

```bash
claims render [flags]
```

**Flags:**

| Flag | Short | Description |
|------|-------|-------------|
| `--api-url` | `-a` | API URL (default: `$CLAIM_API_URL` or `http://localhost:8080`) |
| `--help` | `-h` | Help for render |

**Examples:**

```bash
# Use default API URL (localhost:8080)
claims render

# Specify custom API URL
claims render --api-url http://api.example.com:8080

# Use environment variable
export CLAIM_API_URL=http://api.example.com:8080
claims render
```

**Workflow:**

1. Connects to the claim-machinery API
2. Fetches and displays available templates
3. Presents an interactive form for template selection
4. Dynamically generates parameter input forms based on template spec
5. Renders the claim via API
6. Displays the rendered YAML output
7. Optionally saves to a file

### version

Print version information.

```bash
claims version
```

## Configuration

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `CLAIM_API_URL` | API base URL | `http://localhost:8080` |

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
├── main.go           # Application entry point
├── cmd/
│   ├── root.go       # Root command setup
│   ├── render.go     # Render command implementation
│   ├── version.go    # Version command
│   └── logo.go       # ASCII logo rendering
├── go.mod            # Go module definition
├── Taskfile.yaml     # Task automation
├── docs/             # Documentation (TechDocs)
└── catalog-info.yaml # Backstage component
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feat/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feat/amazing-feature`)
5. Open a Pull Request
