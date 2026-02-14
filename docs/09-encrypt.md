# Phase 9: SOPS-Encrypted Kubernetes Secrets

## Goal

Add a `claims encrypt` command that creates **SOPS-encrypted Kubernetes Secrets** (age encryption) through the same interactive/non-interactive workflow as `render` and `delete`. Encrypted secrets can be safely stored in Git for GitOps controllers (KSOPS, sops-secrets-operator) to decrypt in-cluster.

---

## Flow

```
Fetch secret template from API
  → Collect secret values (hidden input)
  → Generate K8s Secret YAML
  → Encrypt with sops CLI (age)
  → Write to disk / commit to Git via PR
```

---

## Prerequisites

1. **sops CLI** — install from [github.com/getsops/sops](https://github.com/getsops/sops)
2. **age key pair** — generate with `age-keygen`
3. **SOPS_AGE_RECIPIENTS** environment variable set to the age public key

```bash
# Generate age key pair
age-keygen -o age-key.txt

# Export the public key
export SOPS_AGE_RECIPIENTS="age1..."

# For decryption, the private key must be available:
# - Default location: ~/.config/sops/age/keys.txt
# - Or via SOPS_AGE_KEY_FILE env var
```

---

## New Package: `internal/sops/`

### `sops.go` — SOPS binary interaction

```go
// CheckSOPSInstalled returns true if the sops binary is on PATH.
func CheckSOPSInstalled() bool

// CheckSOPSAvailable verifies sops binary + SOPS_AGE_RECIPIENTS env var.
func CheckSOPSAvailable() (recipients string, err error)

// Encrypt encrypts plaintext YAML using sops with age encryption.
func Encrypt(plaintext []byte, recipients string) ([]byte, error)
```

### `secret.go` — K8s Secret YAML generation

```go
type SecretData struct {
    Name       string
    Namespace  string
    StringData map[string]string
}

// GenerateSecretYAML produces a Kubernetes Secret manifest.
func GenerateSecretYAML(data SecretData) ([]byte, error)
```

Generated output:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: <name>
  namespace: <namespace>
type: Opaque
stringData:
  key1: value1
  key2: value2
```

---

## Command Structure

Follows the same pattern as `delete`:

| File | Purpose |
|------|---------|
| `cmd/encrypt_types.go` | `EncryptConfig`, `EncryptResult` structs |
| `cmd/encrypt.go` | Cobra command, flags, `runEncrypt()` dispatch |
| `cmd/encrypt_interactive.go` | Interactive flow with huh forms |
| `cmd/encrypt_noninteractive.go` | CI/CD non-interactive flow |
| `cmd/encrypt_git.go` | Git operations, registry update, PR |

---

## Flags

```bash
claims encrypt [flags]

# Template & Secret
  -a, --api-url string           API URL (default: $CLAIM_API_URL or http://localhost:8080)
  -t, --template string          Template name to use
      --name string              Secret name
      --namespace string         Secret namespace

# Parameter Input
  -f, --params-file string       YAML/JSON file with parameters
  -p, --param strings            Inline param (key=value, repeatable)

# Output Control
  -o, --output-dir string        Output directory (default: .)
      --filename-pattern string  Filename pattern (default: {{.name}}-secret.enc.yaml)
      --dry-run                  Show encrypted output without writing files

# Mode Control
  -i, --interactive              Force interactive mode
      --non-interactive          Force non-interactive mode

# Git & PR (same as render/delete)
      --git-branch string        Branch to use/create
      --git-create-branch        Create the branch if it doesn't exist
      --git-message string       Commit message
      --git-remote string        Git remote (default: origin)
      --git-repo-url string      Clone from URL
      --git-user string          Git username
      --git-token string         Git token
      --create-pr                Create PR after push
      --pr-title string          PR title
      --pr-description string    PR description
      --pr-labels strings        PR labels
      --pr-base string           Base branch for PR (default: main)
```

---

## Interactive Workflow

1. **Check SOPS prereqs** — verify `sops` binary and `SOPS_AGE_RECIPIENTS`
2. **Prompt/confirm API URL** — reuses `promptAPIURL()`
3. **Fetch templates** — from claim-machinery API
4. **Template selection** — single-select form
5. **Secret metadata** — name + namespace inputs with K8s naming validation
6. **Collect secret values** — for each template parameter:
   - `EchoMode(huh.EchoModePassword)` for hidden params
   - Normal input for non-hidden params
   - Respects `Required`, `Default`, `Enum` attributes
7. **Generate Secret YAML** — `sops.GenerateSecretYAML()`
8. **Preview** — pre-encryption YAML in styled box + confirm
9. **Encrypt** — `sops.Encrypt()` with progress
10. **Output config** — reuses `runDestinationChoice()`, `selectDirectory()`
11. **Write encrypted file** — default: `{{.name}}-secret.enc.yaml`
12. **Update registry** — entry with `Source: "cli-encrypt"`
13. **Git ops** — reuses `runGitDetailsForm()`, `runPROptionsForm()`

---

## Non-Interactive Mode

Required flags: `--template`, `--name`, `--namespace`, plus `--params-file` or `--param`.

```bash
# With inline params
claims encrypt --non-interactive \
  --template my-secret-template \
  --name app-secrets \
  --namespace production \
  --param db_password=secret123 \
  --param api_key=abc-def-ghi \
  -o ./secrets

# With params file
claims encrypt --non-interactive \
  --template my-secret-template \
  --name db-credentials \
  --namespace default \
  -f tests/sops-secret-params.yaml \
  -o ./secrets

# Dry run
claims encrypt --non-interactive \
  --template my-secret-template \
  --name test-secret \
  --namespace default \
  --param key=value \
  --dry-run
```

---

## Registry Integration

Encrypted secrets are tracked in `claims/registry.yaml` with:

```yaml
- name: my-app-secret
  template: my-secret-template
  category: secrets
  namespace: production
  createdAt: "2026-02-14T10:30:00Z"
  createdBy: cli-encrypt
  source: cli-encrypt
  path: secrets/my-app-secret-secret.enc.yaml
  status: active
```

---

## Git & PR Integration

Same workflow as `render` and `delete`:

- Branch creation, staging, commit, push
- Auto-generated commit messages: `Add encrypted secret: <namespace>/<name>`
- Auto-generated PR descriptions with template, secret name, namespace

---

## Verification

```bash
# Build
go build ./...

# Unit tests
go test ./internal/sops/...

# All tests
go test ./...

# Manual interactive test
export SOPS_AGE_RECIPIENTS="age1..."
claims encrypt

# Manual non-interactive test
claims encrypt --non-interactive \
  --template <template-name> \
  --name test-secret \
  --namespace default \
  --param key=value \
  --dry-run

# Verify roundtrip
sops --decrypt <file>.enc.yaml
```

---

## Files Created

| File | Action |
|------|--------|
| `internal/sops/sops.go` | Create |
| `internal/sops/secret.go` | Create |
| `internal/sops/sops_test.go` | Create |
| `cmd/encrypt_types.go` | Create |
| `cmd/encrypt.go` | Create |
| `cmd/encrypt_interactive.go` | Create |
| `cmd/encrypt_noninteractive.go` | Create |
| `cmd/encrypt_git.go` | Create |
