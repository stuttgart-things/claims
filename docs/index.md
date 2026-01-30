# claims

terminal-based user for rendering claims

## Overview

This is a Go service created from the Backstage golang-service template.

## Getting Started

### Prerequisites

- Go 1.25.6+
- [Task](https://taskfile.dev/) (optional)

### Installation

```bash
git clone https://github.com/stuttgart-things/claims.git
cd claims
go mod tidy
```

### Running

```bash
# Using go directly
go run .

# Using Task
task run
```

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
