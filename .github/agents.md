# AI Agent Instructions for claims

## Project Overview

- **Name**: claims
- **Description**: terminal-based user for rendering claims
- **Language**: Go 1.25.6
- **Module**: github.com/stuttgart-things/claims

## Project Structure

```
.
├── main.go           # Application entry point
├── go.mod            # Go module definition
├── Taskfile.yaml     # Task automation (use `task --list`)
├── docs/             # TechDocs documentation
├── mkdocs.yml        # MkDocs configuration
└── catalog-info.yaml # Backstage component definition
```

## Development Guidelines

### Code Style

- Follow standard Go conventions and `go fmt`
- Use meaningful variable and function names
- Keep functions small and focused
- Handle errors explicitly, don't ignore them
- Use `log` package for logging

### Project Conventions

- Entry point is in `main.go` with a `run()` function pattern
- Use Task for automation (`task build`, `task test`, `task lint`)
- Dependencies are managed via `go mod`

### Testing

- Place tests in `*_test.go` files alongside the code
- Run tests with `task test` or `go test ./...`
- Aim for meaningful test coverage

### Git Workflow

- Branch naming: `feat/*`, `fix/*`, `docs/*`
- Commits should be atomic and descriptive
- PRs require passing lint checks

## Common Tasks

| Command | Description |
|---------|-------------|
| `task run` | Run the application |
| `task build` | Build binary to ./bin/ |
| `task test` | Run all tests |
| `task lint` | Run golangci-lint |
| `task fmt` | Format code |
| `task tidy` | Tidy go modules |

## When Making Changes

1. Understand the existing code before modifying
2. Follow existing patterns and conventions
3. Add tests for new functionality
4. Run `task lint` before committing
5. Update documentation if needed
