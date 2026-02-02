# claims

Terminal-based CLI for rendering claims via the claim-machinery API.

<details>
<summary><strong>DEV</strong></summary>

### Prerequisites

- Go 1.25.5+
- [Task](https://taskfile.dev/) (optional, für Task-Automatisierung)
- claim-machinery API läuft (Standard: `http://localhost:8080`)

### CLONE + RUN

```bash
# Repository klonen
git clone https://github.com/stuttgart-things/claims.git
cd claims

# Abhängigkeiten installieren
go mod tidy

# Bauen und starten
go build -o claims .
./claims render
```

</details>

## Commands

| Command | Description |
|---------|-------------|
| `claims render` | Interactively render a claim template via API |
| `claims version` | Print version information |

### render

```bash
claims render [flags]
```

| Flag | Short | Description |
|------|-------|-------------|
| `--api-url` | `-a` | API URL (default: `$CLAIM_API_URL` or `http://localhost:8080`) |
| `--templates` | `-t` | Templates to render (comma-separated or repeated) |
| `--output-dir` | `-o` | Output directory for rendered files (default: `/tmp`) |
| `--dry-run` | | Print output without writing files |
| `--single-file` | | Combine all resources into one file |
| `--filename-pattern` | | Pattern for output filenames (default: `{{.template}}-{{.name}}.yaml`) |

**Examples:**

```bash
# Use default API URL
claims render

# Custom API URL
claims render --api-url http://api.example.com:8080

# Via environment variable
CLAIM_API_URL=http://api.example.com:8080 claims render

# Save to custom directory
claims render -o ./manifests

# Dry run (preview without writing)
claims render --dry-run

# Combine all resources into single file
claims render -o ./out --single-file

# Custom filename pattern
claims render -o ./out --filename-pattern "{{.name}}.yaml"

# Render multiple templates (interactive params for each)
claims render -t vsphere-vm -t postgres-db

# Render multiple templates (comma-separated)
claims render -t vsphere-vm,postgres-db -o ./out
```

## Interactive Workflow

The `claims render` command follows an interactive workflow:

1. **API URL** - Confirm or change the API endpoint
2. **Template Selection** - Multi-select templates to render (space to select, enter to confirm)
3. **Parameter Input** - Fill in parameters for each selected template
4. **Render** - Call the API to generate YAML
5. **Review** - Preview rendered resources with options to:
   - Continue to save
   - Edit a template's parameters (re-renders after editing)
   - Cancel the operation
6. **Output** - Configure where and how to save files

## Configuration

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `CLAIM_API_URL` | API base URL | `http://localhost:8080` |

## Available Tasks

```bash
task --list
```

| Task | Description |
|------|-------------|
| `task build` | Build the binary |
| `task run` | Run the application |
| `task test` | Run tests |
| `task test-coverage` | Run tests with coverage report |
| `task lint` | Run golangci-lint |
| `task fmt` | Format code |
| `task tidy` | Tidy and verify go modules |
| `task clean` | Clean build artifacts |

## Project Structure

```
.
├── main.go                    # Application entry point
├── cmd/
│   ├── root.go                # Root command setup
│   ├── render.go              # Render command and flags
│   ├── render_interactive.go  # Interactive form-based rendering
│   ├── render_review.go       # Review/preview step before saving
│   ├── render_output.go       # File output logic (separate/single file, dry-run)
│   ├── render_types.go        # Type definitions for render config/results
│   ├── version.go             # Version command
│   └── logo.go                # ASCII logo rendering
├── internal/
│   └── templates/
│       ├── types.go           # API data models
│       ├── client.go          # HTTP client for claim-machinery API
│       └── client_test.go     # Client unit tests
├── go.mod                     # Go module definition
├── Taskfile.yaml              # Task automation
├── catalog-info.yaml          # Backstage component definition
└── README.md                  # This file
```

## License

See [LICENSE](LICENSE) for details.

## Author

patrick hermann
