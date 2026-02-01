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

**Examples:**

```bash
# Use default API URL
claims render

# Custom API URL
claims render --api-url http://api.example.com:8080

# Via environment variable
CLAIM_API_URL=http://api.example.com:8080 claims render
```

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
├── main.go           # Application entry point
├── cmd/
│   ├── root.go       # Root command setup
│   ├── render.go     # Render command implementation
│   ├── version.go    # Version command
│   └── logo.go       # ASCII logo rendering
├── go.mod            # Go module definition
├── Taskfile.yaml     # Task automation
├── catalog-info.yaml # Backstage component definition
└── README.md         # This file
```

## License

See [LICENSE](LICENSE) for details.
