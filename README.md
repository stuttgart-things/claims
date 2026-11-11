# claims

Terminal-based CLI for rendering claims via the claim-machinery API.

| | |
|---|---|
| **Docs** | [stuttgart-things.github.io/claims](https://stuttgart-things.github.io/claims/) |
| **Releases** | [GitHub Releases](https://github.com/stuttgart-things/claims/releases) |
| **Container** | `ghcr.io/stuttgart-things/claims` |

<details>
<summary><strong>DEV</strong></summary>

### Prerequisites

- Go 1.25.5+
- [Task](https://taskfile.dev/) (optional
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
| `claims render` | Render claim templates, save locally or via Git PR |
| `claims encrypt` | Create a SOPS-encrypted Kubernetes Secret via Git PR |
| `claims delete` | Delete a claim via Git PR |
| `claims change` (alias `resize`) | Change a claim (VM size, Ansible re-run) via Git PR |
| `claims merge` | Merge an order, change or deletion PR (squash) after its checks |
| `claims list` | List claims (registry and `*-xrs` directories) |
| `claims login` | Log in to claim-machinery-api with ZITADEL (device flow) |
| `claims logout` | Forget the cached login for an API URL |
| `claims whoami` | Show the user, roles and groups claims sends to the API |
| `claims version` | Print version information |

### render

```bash
claims render [flags]
```

| Flag | Short | Description |
|------|-------|-------------|
| `--api-url` | `-a` | API URL (default: `$CLAIM_API_URL` or `http://localhost:8080`) |
| `--api-token` | | Bearer token for claim-machinery-api (default: `$CLAIM_API_TOKEN`); see [API Authentication](#api-authentication) |
| `--api-oidc-token-url` | | OIDC token endpoint for client credentials (default: `$CLAIM_API_OIDC_TOKEN_URL`) |
| `--api-oidc-client-id` | | OIDC client ID (default: `$CLAIM_API_OIDC_CLIENT_ID`) |
| `--api-oidc-client-secret` | | OIDC client secret (default: `$CLAIM_API_OIDC_CLIENT_SECRET`) |
| `--api-oidc-scope` | | OIDC scope(s), space- or comma-separated (default: `$CLAIM_API_OIDC_SCOPE`) |
| `--templates` | `-t` | Templates to render (comma-separated or repeated) |
| `--params` | `-p` | Parameters as key=value pairs (comma-separated or repeated) |
| `--params-file` | `-f` | YAML file with templates and parameters for batch rendering |
| `--output-dir` | `-o` | Output directory for rendered files (default: `/tmp`) |
| `--dry-run` | | Print output without writing files |
| `--stdout` | | Print the rendered YAML to stdout, write nothing (non-interactive; progress on stderr) |
| `--full` | | Interactive review: show the complete YAML instead of the first 15 lines |
| `--single-file` | | Combine all resources into one file |
| `--filename-pattern` | | Pattern for output filenames (default: `{{.template}}-{{.name}}.yaml`) |
| `--file-mode` | | File write mode: `overwrite` (default) or `append` |
| `--non-interactive` | | Run in non-interactive mode (for CI/CD automation) |
| `--git-commit` | | Commit rendered files to git |
| `--git-push` | | Push commits to remote (implies `--git-commit`) |
| `--git-branch` | | Branch to use/create |
| `--git-create-branch` | | Create the branch if it doesn't exist |
| `--git-message` | | Commit message (default: auto-generated) |
| `--git-remote` | | Git remote name (default: `origin`) |
| `--git-repo-url` | | Clone from URL instead of using local repo |
| `--git-user` | | Git username (or `$GIT_USER` env) |
| `--git-token` | | Git token (or `$GIT_TOKEN`/`$GITHUB_TOKEN` env) |
| `--create-pr` | | Create a pull request after push |
| `--pr-title` | | PR title (default: auto-generated) |
| `--pr-description` | | PR description |
| `--pr-labels` | | PR labels (comma-separated) |
| `--pr-base` | | Base branch for PR (default: `main`) |

**Examples:**

```bash
# Use default API URL
claims render

# Custom API URL
claims render --api-url http://api.example.com:8080

# Via environment variable
CLAIM_API_URL=http://api.example.com:8080 claims render

# Multiple API endpoints (interactive: shows selector, non-interactive: uses first)
CLAIM_API_URL=http://dev-api:8080:http://staging-api:8080:http://prod-api:8080 claims render

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

### Append Mode

Use `--file-mode=append` to build multi-resource YAML files across multiple render calls:

```bash
# First render creates the file
claims render --non-interactive \
  -t flux-gitrepository \
  -p name=flux-infra -p url=https://github.com/stuttgart-things/flux.git \
  -o ./gitrepos --filename-pattern "gitrepositories.yaml"

# Second render appends with --- separator
claims render --non-interactive \
  -t flux-gitrepository \
  -p name=flux-apps -p url=https://github.com/stuttgart-things/flux.git \
  -o ./gitrepos --filename-pattern "gitrepositories.yaml" \
  --file-mode=append
```

Result (`gitrepos/gitrepositories.yaml`):

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: flux-infra
  namespace: flux-system
spec:
  url: https://github.com/stuttgart-things/flux.git
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: flux-apps
  namespace: flux-system
spec:
  url: https://github.com/stuttgart-things/flux.git
```

### Multiple API Endpoints

`CLAIM_API_URL` supports colon-separated multiple endpoints. In interactive mode, a selector is shown. In non-interactive mode, the first endpoint is used.

```bash
# Configure multiple claim-machinery-api instances
export CLAIM_API_URL=http://dev-api:8080:http://staging-api:8080:http://prod-api:8080
claims render
```

Interactive prompt:

```
? Select API endpoint
> http://dev-api:8080
  http://staging-api:8080
  http://prod-api:8080
```

Non-interactive (uses first endpoint):

```bash
CLAIM_API_URL=http://dev-api:8080:http://staging-api:8080 \
  claims render --non-interactive -t my-template -p name=foo
# Connects to: http://dev-api:8080
```

URL colons in `http://`, `https://`, and port numbers are handled correctly — only bare `:` between URLs acts as a separator.

### API Authentication

claim-machinery-api v0.25.0+ can require a bearer token (ZITADEL JWT) on `POST /api/v1/claim-templates/{name}/order` when the server sets `AUTH_OIDC_ISSUER`; read endpoints stay open. Authentication in `claims` is **off by default** — no `Authorization` header is sent unless one of the following is configured. It applies to `render` (interactive and non-interactive) and `encrypt`; flags take precedence over environment variables.

Order of precedence: `--api-token` / `CLAIM_API_TOKEN` > OAuth2 client credentials (`CLAIM_API_OIDC_*`) > cached `claims login` for the API URL.

**Interactive login** (humans) — OAuth2 device flow against the lab ZITADEL; the tokens are cached per API URL in `~/.config/claims/credentials.json` (mode 0600, override with `CLAIMS_CREDENTIALS_FILE`) and refreshed automatically:

```bash
# --issuer / --client-id / --scope or CLAIM_API_LOGIN_ISSUER / _CLIENT_ID / _SCOPE
claims login --api-url https://claims.example.com/claims \
  --issuer https://zitadel.example.com \
  --client-id 123456789012345678@claims \
  --scope "openid profile email offline_access urn:zitadel:iam:org:project:id:<project-id>:aud urn:zitadel:iam:org:projects:roles"
# open the printed URL, enter the code, approve

claims whoami --api-url https://claims.example.com/claims    # user, roles, groups, requested_by ref
claims render --api-url https://claims.example.com/claims    # sends the cached token
claims logout --api-url https://claims.example.com/claims    # or --all
```

- One login per API URL: each lab may run its own ZITADEL. A second `claims login` for the same URL reuses issuer, client ID and scope of the first.
- `--ca-file` / `CLAIM_API_LOGIN_CA_FILE` adds PEM roots for an issuer with a private CA (the labs' Vault PKI).
- Without `offline_access` no refresh token is issued and the login ends with the access token.
- `claims` never sets `requested_by` itself; with auth on, claim-machinery-api stamps it from the token (`user:default/<preferred_username>`), which `claims whoami` shows.
- Setting up the ZITADEL application: see [docs/10-login.md](docs/10-login.md).

**Static token** — sent as `Authorization: Bearer <token>` on every API request:

```bash
export CLAIM_API_TOKEN="$(cat ~/.config/claims/token)"   # or --api-token
claims render --non-interactive -f params.yaml
```

**OAuth2 client credentials** (pipelines) — the token is fetched from the IdP once and cached in-process until it expires:

```bash
export CLAIM_API_OIDC_TOKEN_URL=https://zitadel.example.com/oauth/v2/token   # --api-oidc-token-url
export CLAIM_API_OIDC_CLIENT_ID=claims-ci                                    # --api-oidc-client-id
export CLAIM_API_OIDC_CLIENT_SECRET=...                                      # --api-oidc-client-secret
export CLAIM_API_OIDC_SCOPE="openid urn:zitadel:iam:org:project:id:<id>:aud" # optional, --api-oidc-scope
claims render --non-interactive -f params.yaml
```

Rules:

- `CLAIM_API_TOKEN` / `--api-token` wins when both a static token and client credentials are set.
- Client credentials need token URL, client ID and client secret; a partial set is rejected with an error naming the missing variables. The scope is optional (space- or comma-separated).
- With several `CLAIM_API_URL` endpoints a static token or client credentials are used for **all** of them; a cached login only for the URL it was made for.
- A `401` from the API prints a hint naming `claims login`, `CLAIM_API_TOKEN` and the `CLAIM_API_OIDC_*` variables; a `403` means the token was accepted but lacks the order role (or the owner is outside your groups) — `claims whoami` shows what the token carries.
- Token values are never printed or logged. Prefer environment variables over `--api-token` so the token does not end up in shell history or process listings.

### Profile-Based Template Selection

When the API serves templates from multiple profiles, interactive mode shows a profile selector first:

```
? Select template profile
> All profiles
  Crossplane Claims
  Flux Kustomizations

? Select templates to render
  [ ] flux-kustomization-clusterbook-app - Clusterbook App Deployment
  [ ] flux-kustomization-cert-manager-install - Flux Kustomization Cert-Manager
```

This requires claim-machinery-api v0.21.0+ with multiple profiles configured via `TEMPLATE_PROFILE_PATH`.

### Non-Interactive Mode (CI/CD)

For automation and CI/CD pipelines, use `--non-interactive` mode:

```bash
# Single template with inline parameters
claims render --non-interactive -t vspherevm -p name=my-vm -p cpu=4 -o ./out

# Multiple parameters (comma-separated)
claims render --non-interactive -t vspherevm -p name=my-vm,cpu=4,memory=8Gi

# Render a clusterbook claim with custom API URL
CLAIM_API_URL=http://localhost:8080 claims render --non-interactive \
  -t cilium-clusterbook \
  -p clusterName=testcluster \
  -p networkKey=10.31.103 \
  -p registerDns=false \
  -o output

# Batch rendering with params file
claims render --non-interactive -f params.yaml -o ./out

# YAML to stdout only (no files, no registry, no git), e.g. to pipe it on
claims render -t vspherevm -p name=my-vm --stdout | kubectl apply --dry-run=client -f -
```

**Params file format (`params.yaml`):**

```yaml
templates:
  - name: vspherevm
    parameters:
      name: my-vm
      cpu: 4
      memory: 8Gi

  - name: postgresql
    parameters:
      name: my-database
      version: "15"
```

### GitOps Integration

Rendered manifests can be automatically committed and pushed to a git repository:

```bash
# Commit rendered files to local git repo
claims render --non-interactive -t volumeclaim-simple -p name=my-volume \
  -o ./manifests --git-commit

# Commit with custom message
claims render --non-interactive -t volumeclaim-simple -p name=my-volume \
  -o ./manifests --git-commit --git-message "Add volume claim for app"

# Create a new branch and commit
claims render --non-interactive -t volumeclaim-simple -p name=my-volume \
  -o ./manifests --git-commit --git-create-branch --git-branch feature/add-volume

# Commit and push to remote
claims render --non-interactive -t volumeclaim-simple -p name=my-volume \
  -o ./manifests --git-push

# Clone-based workflow (for CI/CD without local checkout)
claims render --non-interactive -t volumeclaim-simple -p name=my-volume \
  --git-repo-url https://github.com/org/gitops-repo.git \
  -o manifests --git-push --git-create-branch --git-branch feature/new-claim
```

**Authentication:**

Git credentials can be provided via flags or environment variables:

```bash
# Via flags
claims render ... --git-push --git-user myuser --git-token ghp_xxx

# Via environment variables
export GIT_USER=myuser
export GIT_TOKEN=ghp_xxx  # or GITHUB_TOKEN for GitHub Actions
claims render ... --git-push
```

### Pull Request Support

Automatically create pull requests after pushing changes:

```bash
# Create PR with auto-generated title/description
claims render --non-interactive -t volumeclaim-simple -p name=my-volume \
  -o ./manifests --create-pr --git-create-branch --git-branch feature/add-volume

# Create PR with custom title and labels
claims render --non-interactive -t volumeclaim-simple -p name=my-volume \
  -o ./manifests --create-pr --git-create-branch --git-branch feature/add-volume \
  --pr-title "Add volume claim for application" \
  --pr-labels "infrastructure,automated"

# Create PR with custom description
claims render --non-interactive -f params.yaml \
  -o ./manifests --create-pr --git-create-branch --git-branch feature/infra-update \
  --pr-title "Infrastructure update" \
  --pr-description "Adding new infrastructure resources" \
  --pr-base main
```

**Requirements:**

claims opens the PR through the GitHub API (claims#108), in the repository of
the checkout's `origin` remote. It needs a token that may create pull requests:
`--git-token`, else `$GIT_TOKEN`, else `$GITHUB_TOKEN`, else the `gh` CLI's
login (`gh auth token`). The `gh` CLI itself is not required.

### Source-Path Layout (`--layout source-path`)

For orders that know where they belong -- the VM orders of
`xr-native-vm` (stuttgart-things/kcl) -- the order decides the files, not
`--filename-pattern`:

- a document annotated `stuttgart-things.com/target-path` goes to that file
  (e.g. a dev VM's ScrapeConfig)
- a document annotated `stuttgart-things.com/source-path` goes to that file
  (the XR: the annotation names the file it lives in)
- a document with neither goes to the file of the document before it (e.g.
  the dev VM's EnvironmentConfig, next to its XR)

Paths are relative to `--output-dir`, which is the repository root. A file
that exists already is refused (the name is taken). If a `kustomization.yaml`
sits next to a file, `  - <file>` is appended as its last line (once;
`resources:` must be the last key); a directory without one -- like today's
`machinery-orders-xrs` -- is applied as a whole and gets nothing. No
`claims/registry.yaml` is written: the order's file with its annotations is
the record. Non-interactive only; templates with secrets are refused.

With `--git-commit`/`--git-push`/`--create-pr` the branch, commit message and
PR follow the Backstage templates, so the repository's checks and release
tooling treat the PR like theirs. A VM order (template type `xr-native-vm`)
gets create-vm's: branch `vm-<name>`, title
`feat(machinery): VM <name> (<use_case>, <lab>) (MERGE = BAU)`. Flags
override them.

```bash
# Preview: every file, the kustomization lines, branch and PR title
claims render --non-interactive --layout source-path -o ~/projects/stuttgart-things \
  -t native-vm-labda-vsphere -p name=my-vm -p use_case=dev -p size=large --dry-run

# Order it: branch vm-my-vm, commit, push, PR
claims render --non-interactive --layout source-path -o ~/projects/stuttgart-things \
  -t native-vm-labda-vsphere -p name=my-vm -p use_case=dev -p size=large --create-pr

# ... and merge it once its checks are green (MERGE = BAU: this builds the VM)
claims render --non-interactive --layout source-path -o ~/projects/stuttgart-things \
  -t native-vm-labda-vsphere -p name=my-vm -p use_case=dev -p size=large --create-pr --merge
```

Start from an up-to-date `main`: the branch is created from the current HEAD.

### list

```bash
claims list [--source all|registry|xrs] [--kind K,...] [--repo DIR] [--registry FILE] [--github OWNER/NAME] [--template T] [--category C] [-o table|json]
```

Lists the claims of the fleet repository from two places: `claims/registry.yaml`
(written by `claims render`) and the XRs in `clusters/<lab>/<infra>/*-xrs/*.yaml`
(e.g. the VM orders in `machinery-orders-xrs/`; TEMPLATE shows the kind, PATH the
file; run-once runs are left out). The repository is the git repository around
the current directory, or `--repo` / `$CLAIMS_REPO`; the registry is
`--registry` / `$CLAIMS_REGISTRY` or `<repo>/claims/registry.yaml`.
`--kind` keeps only XRs of the given kinds (comma-separated or repeated,
case-insensitive), e.g. `--kind NativeVsphereVM,NativeProxmoxVM` for the VMs;
registry entries have no kind and are left out (noted on stderr).

Without a checkout of the fleet repository, and without `--repo` / `--registry`,
claims list reads both from GitHub at `main`: `--github`, else
`$CLAIMS_GITHUB_REPO`, else `stuttgart-things/stuttgart-things`. A git checkout
without `*-xrs` directories, such as claims itself, does not count as the fleet
repository. See [Without a checkout](#without-a-checkout-of-the-fleet-repository).

### delete: XRs in `*-xrs` directories

A claim (XR) that lives in an `*-xrs` directory of the fleet repository (`clusters/<lab>/<infra>/<x>-xrs/`, e.g. the machinery orders) is deleted **file first**, the same way as Backstage's `request-claim-deletion` / `delete-claim` (see the repo's `clusters/labda/vsphere/machinery-xrs/README.md`):

1. `claims delete` removes the claim's file, the run-once `ScheduledRun`s that apply to it (`stuttgart-things.com/applies`) and their `kustomization.yaml` entries (orders directories have none), on the branch `delete-claim/<name>`, and opens the PR `Delete claim: <Kind>/<name>` with the label `delete-claim`.
2. The fleet repository's `pr-claim-delete.yaml` gates that PR (`hack/claim-deletion.py verify-pr`).
3. After the merge, `delete-claim-xr.yaml` waits for Flux and deletes the object on the management cluster.

`claims` never deletes anything on a cluster. It applies the rules of `hack/claim-deletion.py` before it writes anything:
- exactly one XR in the file
- the claim's `stuttgart-things.com/source-path` is the file itself
- a path inside an `*-xrs` directory
- no ClusterStack
- each removed file listed exactly once in `kustomization.yaml`

```bash
cd ~/projects/stuttgart-things && git checkout main && git pull

# by kind + name (searched in all *-xrs directories) ...
claims delete --kind NativeVsphereVM --name dev7-vm --reason "project ended" --dry-run
claims delete --kind NativeVsphereVM --name dev7-vm --reason "project ended" --create-pr

# ... or by the file (its source-path)
claims delete --source-path clusters/labda/vsphere/machinery-orders-xrs/dev7.yaml --create-pr

# interactive: pick from the *-xrs claims (automatic in a repo without claims/registry.yaml)
claims delete --xr
```

| Flag | Description |
|------|-------------|
| `--kind`, `--name`, `--namespace` | The XR (namespace default `default`); kind is optional when the name is unique |
| `--source-path` | The XR's file instead of kind/name |
| `--reason` | Shown in the PR body |
| `--requested-by` | Shown in the PR body (default: the git user) |
| `--create-pr` | Commit, push `delete-claim/<name>` and open the PR (needs `GIT_USER`/`GIT_TOKEN`) |
| `--git-repo-url` | Work on a fresh clone instead of the local checkout |
| `--dry-run` | Show file, runs, kustomization entries, branch and title |
| `--merge` | With `--create-pr`: wait for the PR's checks, then squash-merge it (see [merge](#merge-finish-an-order-change-or-deletion-pr)). Merging starts the deletion |

Without `--create-pr` the files are only removed locally. `--git-branch` may only name `delete-claim/...`. In a local checkout you must be on `main` (the PR base): the gate accepts nothing but the claim's removal on top of it.

### change: resize a VM claim or re-run its Ansible

`claims change` resizes a VM claim (`NativeVsphereVM`, `NativeProxmoxVM`) in an `*-xrs` directory. It is a thin wrapper around the fleet repository's `dispatch-change-claim.yaml`, the workflow behind Backstage's `request-claim-change`. The rules stay in `hack/claim-change.py`:
- cpu, memory and disk come from create-vm's size table
- a disk never shrinks
- only the size may change

claims resolves the claim, starts the workflow with the same inputs, waits for it and prints the PR `change-claim/<name>`, which `pr-claim-change.yaml` checks. Merging the PR applies the size.

```bash
claims change --name dev7-vm --size xlarge --dry-run
claims change --name dev7-vm --size xlarge --reboot evening --reason "builds run out of memory"
claims change --source-path clusters/labda/vsphere/machinery-orders-xrs/dev7.yaml --size xlarge --reboot 2026-11-05T20:00
claims change -i                   # pick VM, size and reboot window
```

#### Re-run a failed AnsibleRun (`--run-id`, `--retry`)

Deleting an AnsibleRun does not re-run it (crossplane-configurations#554). A new `spec.ansible.runID` does: Crossplane renders the AnsibleRun afresh from the current spec and EnvironmentConfig defaults, so a fixed collection is picked up, and starts a new PipelineRun `<name>-ansible-<runID>`. `claims change` opens that change as a PR `change-claim/<name>`, on the same path as a resize.

```bash
claims change dev7-vm --retry --dry-run                       # unset -> 2, a number -> +1
claims change dev7-vm --retry --reason "baseos collection fixed"
claims change dev7-vm --run-id fix-docker                     # a chosen id
```

> **Read the failed run's logs before merging.** The merge deletes the previous PipelineRun and its TaskRun logs. claims and the PR body name the PipelineRun.

- The claim's `spec.ansible.enabled` must be `true`, and the id must be new and match `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`.
- `--retry` on a non-numeric runID is refused: pass the next one with `--run-id`.
- claims checks this up front and shows `current -> new`, from the checkout or, without one, from GitHub at `--ref`. The workflow checks again against `main`.
- One change per PR: `--size` and `--run-id`/`--retry` exclude each other, and `--reboot` only goes with `--size`.
- Needs vspherevm >= v0.14.0 / proxmoxvm >= v0.20.0 on the management cluster (crossplane-configurations#555). An older XRD rejects the field, and Flux reports the apply as failed.

| Flag | Description |
|------|-------------|
| `--kind`, `--name`, `--namespace`, `--source-path` | The VM claim (or the argument: a name, or a path ending in `.yaml`). The name is looked up in the checkout, or on GitHub without one |
| `--size` | New size: `small`, `medium`, `large` or `xlarge` (checked by the workflow) |
| `--run-id`, `--retry` | Re-run the AnsibleRun with this `spec.ansible.runID`, or the next one (instead of `--size`) |
| `--reboot` | When the one-shot run applies the resize in the guest: `evening` (19-22 Europe/Berlin, default), `now`, `none`, or an ISO-8601 time |
| `--reason`, `--requested-by` | Shown in the PR body. `--requested-by` defaults to the git user, then the token's GitHub login |
| `--repo`, `--ref` | Fleet repository (default: the checkout's; without one `$CLAIMS_GITHUB_REPO` or `stuttgart-things/stuttgart-things`) and the branch the workflow runs on and the claim is read at (default `main`) |
| `--dry-run`, `--no-wait` | Show the inputs only; or start the workflow and return without waiting |
| `--merge` | Wait for the PR's checks, then squash-merge it (see [merge](#merge-finish-an-order-change-or-deletion-pr)); not with `--no-wait`, and not with `--retry`/`--run-id` (merging deletes the previous PipelineRun's logs: read them, then `claims merge`) |

When the workflow refuses the change, for example with an unknown size, a size the VM already has, or a kind it does not resize, claims prints the workflow's `::error::` lines (the failed job's annotations).

claims talks to the GitHub API directly (claims#108): the token (see below) needs permission to run workflows (`actions: write`) and to read the repository. The `gh` CLI is not required. Outside a checkout, and in a checkout whose `origin` is not on GitHub, the workflow runs in `--repo`, `$CLAIMS_GITHUB_REPO` or `stuttgart-things/stuttgart-things`.

### merge: finish an order, change or deletion PR

`render --create-pr`, `change` and `delete --create-pr` open a PR and stop. `claims merge` finishes it (claims#113): it waits for the checks, squash-merges the PR and deletes its branch.

```bash
claims merge dev7 --dry-run                     # show the PR, its files and checks
claims merge vm-dev7 --wait                     # wait for the checks, ask, merge
claims merge delete-claim/dev7-vm --wait --yes  # no question (CI)
claims merge 4442                               # by number, or by URL
```

The argument can be one of these:
- a PR number or URL
- a branch: `vm-<name>`, `change-claim/<name>`, `delete-claim/<name>` or `claim-<template>-<name>`
- a name, which claims looks up as `vm-<name>`, `change-claim/<name>` and `delete-claim/<name>`

A VM's XR name `<name>-vm` finds its order `vm-<name>` too. When a name matches several open PRs, claims lists them; name one by number or branch then.

claims shows the PR's title, branch, labels, files, the required checks and the state of the checks. Then it asks, repeating what the merge does: **MERGE = BAU** builds the VM, a `delete-claim` PR deletes the object, a `change-claim` PR applies the change. `--yes` skips the question; without a terminal `--yes` is required.

**Checks.** These are the base branch's required status checks (branch protection and rulesets). When the branch requires none, every check on the PR counts. The fleet repository's `main` has no branch protection today, so every check counts there. A failed check stops the merge. Without `--wait`, a check that still runs stops it too. `--wait` waits for the checks, up to `--timeout` (default 30m), and stops at the first failure. When the branch moves on while claims waits, claims waits for the checks of the new head. The merge itself is pinned to the head whose checks claims saw.

**Guard rails.** The merge is the human approval of an order, and claims does not weaken it:
- claims only merges PRs on its own branches (above), from the repository itself (no forks), into the default branch. Arbitrary PRs are refused.
- claims never bypasses branch protection. It merges only when GitHub reports the PR mergeable (`mergeable_state` `clean`, `unstable` or `has_hooks`). A `blocked` PR (a review or a required check missing), a PR with conflicts and one that is behind are refused, even when the token could merge as an admin.
- When GitHub refuses the merge, claims prints GitHub's reason. A failed check, a timeout, a refusal or a "No" at the prompt always leaves the PR open.

**`--merge` on render, change and delete.** With `--merge`, `render --create-pr`, `change` and `delete --create-pr` wait for the PR they just opened and merge it in the same call. `--merge` implies `--wait`. The flag is the confirmation, so there is no question. The same checks and guard rails apply. Because claims opened that PR itself, a `--git-branch` of your own is accepted too.

The token (`$GIT_TOKEN`, `$GITHUB_TOKEN`, else the `gh` CLI's login) needs permission to merge PRs and delete branches (`contents: write`, `pull_requests: write`). `--repo` defaults to the checkout's GitHub repository; outside one, it is `$CLAIMS_GITHUB_REPO` or `stuttgart-things/stuttgart-things`.

### Without a checkout of the fleet repository

`claims change` and `claims list` work from any directory (claims#107). When
the current directory is not in a checkout with `*-xrs` directories, they read
the fleet repository straight from GitHub: one call for the tree, then the
`*-xrs` files in parallel. No clone needed.

```bash
cd /tmp
claims list --kind NativeProxmoxVM
claims change sbx-labul-vm --retry --dry-run   # runID read from main
```

- Repository: `--repo` (change) / `--github` (list), else `$CLAIMS_GITHUB_REPO`, else `stuttgart-things/stuttgart-things`.
- Token: `$GIT_TOKEN`, else `$GITHUB_TOKEN`, else the `gh` CLI's login (`gh auth token`), the same for every GitHub call claims makes. The repository is private, so there is no anonymous fallback.
- `$GITHUB_API_URL` points claims at another API endpoint (GitHub Enterprise).
- A checkout still wins when there is one. It may be behind `main` or on another branch; then `git pull` first, or run claims outside it.
- `claims delete` still needs a checkout: it writes and commits the removal itself.

### encrypt

Create SOPS-encrypted Kubernetes Secrets using age encryption. Fetches a template from the API, collects secret values, generates a K8s Secret YAML, encrypts it with SOPS, and optionally commits via Git PR.

```bash
claims encrypt [flags]
```

| Flag | Short | Description |
|------|-------|-------------|
| `--api-url` | `-a` | API URL (default: `$CLAIM_API_URL` or `http://localhost:8080`) |
| `--api-token` | | Bearer token for claim-machinery-api (default: `$CLAIM_API_TOKEN`); see [API Authentication](#api-authentication) |
| `--api-oidc-token-url` | | OIDC token endpoint for client credentials (default: `$CLAIM_API_OIDC_TOKEN_URL`) |
| `--api-oidc-client-id` | | OIDC client ID (default: `$CLAIM_API_OIDC_CLIENT_ID`) |
| `--api-oidc-client-secret` | | OIDC client secret (default: `$CLAIM_API_OIDC_CLIENT_SECRET`) |
| `--api-oidc-scope` | | OIDC scope(s), space- or comma-separated (default: `$CLAIM_API_OIDC_SCOPE`) |
| `--template` | `-t` | Template name to use |
| `--name` | | Secret name |
| `--namespace` | | Secret namespace |
| `--params-file` | `-f` | YAML/JSON file with parameters |
| `--param` | `-p` | Inline param (key=value, repeatable) |
| `--output-dir` | `-o` | Output directory (default: `.`) |
| `--filename-pattern` | | Filename pattern (default: `{{.name}}-secret.enc.yaml`) |
| `--dry-run` | | Show encrypted output without writing files |
| `--interactive` | `-i` | Force interactive mode |
| `--non-interactive` | | Force non-interactive mode |
| `--git-branch` | | Branch to use/create |
| `--git-create-branch` | | Create the branch if it doesn't exist |
| `--git-message` | | Commit message (default: auto-generated) |
| `--git-remote` | | Git remote name (default: `origin`) |
| `--git-repo-url` | | Clone from URL instead of using local repo |
| `--git-user` | | Git username (or `$GIT_USER`/`$GITHUB_USER` env) |
| `--git-token` | | Git token (or `$GIT_TOKEN`/`$GITHUB_TOKEN` env) |
| `--create-pr` | | Create a pull request after push |
| `--pr-title` | | PR title (default: auto-generated) |
| `--pr-description` | | PR description |
| `--pr-labels` | | PR labels (comma-separated) |
| `--pr-base` | | Base branch for PR (default: `main`) |

**Prerequisites:**

- [sops](https://github.com/getsops/sops) CLI installed
- `SOPS_AGE_RECIPIENTS` environment variable set (age public key)

**Examples:**

```bash
# Interactive mode (prompts for template, secret name, values)
claims encrypt

# Non-interactive with inline params
claims encrypt --non-interactive \
  --template my-secret-template \
  --name my-app-secret \
  --namespace production \
  --param username=admin \
  --param password=s3cret

# Non-interactive with params file
claims encrypt --non-interactive \
  --template my-secret-template \
  --name db-credentials \
  --namespace default \
  -f examples/encrypt-params.yaml \
  -o ./secrets

# Dry run (preview without writing)
claims encrypt --non-interactive \
  --template my-secret-template \
  --name my-app-secret \
  --namespace default \
  --param key=value \
  --dry-run

# Encrypt and create PR
claims encrypt --non-interactive \
  --template my-secret-template \
  --name staging-secrets \
  --namespace staging \
  --param db_password=secret123 \
  --create-pr --git-create-branch --git-branch feat/add-staging-secrets \
  --pr-labels "secrets,automated"
```

## Interactive Workflow

The `claims render` command follows an interactive workflow:

1. **API URL** - Confirm or change the API endpoint
2. **Template Selection** - Multi-select templates to render (space to select, enter to confirm)
3. **Parameter Input** - Fill in parameters for each selected template
4. **Render** - Call the API to generate YAML
5. **Review** - Preview rendered resources with options to:
   - Continue to save
   - Show full YAML (through `$PAGER`, default `less -FRX`; or start with `--full`)
   - Edit a template's parameters (re-renders after editing)
   - Cancel the operation
6. **Output** - Configure where and how to save files. When every rendered
   document names its file (`stuttgart-things.com/source-path`/`target-path`),
   the cwd is in a git repository and no template defines secrets, the first,
   preselected choice is **Fleet repo by source-path**: the same layout as
   `--layout source-path` (files at their annotated paths, kustomization
   entries, no registry entry, branch/PR title like the Backstage templates),
   either with commit, push and PR or as files only. Otherwise (or with
   **Local**) the directory picker starts in the directory of the order's
   `source-path` annotation when there is one.
7. **Git Operations** - Optionally commit/push rendered files:
   - None (just save locally)
   - Commit to current branch
   - Commit to new branch
   - Commit and push to remote
   - Commit, push & create PR (with PR details form)

Ctrl+C / Esc aborts with `Aborted, nothing written.` and exit code 130. After
rendering, the abort offers to print the YAML or save it to a temp file first.

## Configuration

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `CLAIM_API_URL` | API base URL (colon-separated for multiple endpoints) | `http://localhost:8080` |
| `CLAIM_API_TOKEN` | Static bearer token for claim-machinery-api (wins over client credentials) | - (no auth) |
| `CLAIM_API_OIDC_TOKEN_URL` | OIDC token endpoint for client-credentials auth | - |
| `CLAIM_API_OIDC_CLIENT_ID` | OIDC client ID for client-credentials auth | - |
| `CLAIM_API_OIDC_CLIENT_SECRET` | OIDC client secret for client-credentials auth | - |
| `CLAIM_API_OIDC_SCOPE` | OIDC scope(s), space- or comma-separated (optional) | - |
| `GIT_USER` | Git username for push operations | - |
| `GIT_TOKEN` | Git token/password for push operations | - |
| `GITHUB_TOKEN` | GitHub token (fallback for `GIT_TOKEN`) | - |
| `CLAIMS_REPO` | Fleet repository root for `claims list` | git root of the cwd |
| `CLAIMS_REGISTRY` | `registry.yaml` for `claims list` | `<repo>/claims/registry.yaml` |
| `NO_COLOR` | No banner (the banner is also left out when stdout is not a terminal) | - |
| `SOPS_AGE_RECIPIENTS` | age public key for SOPS encryption (required for `encrypt`) | - |

## Available Tasks

```bash
task --list
```

| Task | Description |
|------|-------------|
| `task build` | Build the binary (Dagger) |
| `task build-local` | `go build` to `./bin/claims` with version, commit and date via ldflags |
| `task install-local` | Unit tests, `build-local`, install to `~/.local/bin` |
| `task render` | Run render in non-interactive mode with params file |
| `task render-inline` | Run render in non-interactive mode with inline params |
| `task test-gitops` | Run GitOps integration tests |
| `task release-check` | Dry-run GoReleaser (releases come from the Release workflow only) |
| `task trigger-release` | Start the Release workflow by hand (no test gate) |

**Testing non-interactive mode:**

```bash
# With params file (default: tests/params.yaml)
task render

# With custom params file
task render PARAMS_FILE=my-params.yaml

# With inline template and params
task render-inline TEMPLATE=vspherevm PARAMS=name=test

# With multiple params
task render-inline TEMPLATE=vspherevm PARAMS="name=test,cpu=4"
```

## Testing

Run all unit tests:

```bash
go test ./...
```

Run tests with verbose output:

```bash
go test ./... -v
```

Run integration tests (requires `CLAIM_API_URL` to be set):

```bash
CLAIM_API_URL=http://localhost:8080 go test -tags=integration ./tests/integration/...
```

Run GitOps shell tests:

```bash
./tests/test_gitops.sh
# or via task
task test-gitops
```

### Test Coverage

| Package | Description |
|---------|-------------|
| `cmd/` | Output logic, review functionality |
| `internal/templates/` | API client with mocked HTTP server |
| `internal/apiauth/` | Bearer-token / client-credentials auth against a fake token endpoint |
| `internal/gitops/` | Git operations, branch management, auth |
| `internal/params/` | Parameter file parsing, inline params |
| `internal/sops/` | SOPS encryption, K8s Secret YAML generation |
| `internal/registry/` | Registry CRUD operations |
| `tests/integration/` | End-to-end render workflow tests |

## Project Structure

```
.
├── main.go                    # Application entry point
├── cmd/
│   ├── root.go                # Root command setup
│   ├── render.go              # Render command and flags
│   ├── render_interactive.go  # Interactive form-based rendering
│   ├── render_noninteractive.go # Non-interactive mode for CI/CD
│   ├── render_review.go       # Review/preview step before saving
│   ├── render_output.go       # File output logic (separate/single file, dry-run)
│   ├── render_output_test.go  # Output logic tests
│   ├── render_review_test.go  # Review functionality tests
│   ├── render_git.go          # Git operations integration
│   ├── render_pr.go           # Pull request creation integration
│   ├── render_types.go        # Type definitions for render config/results
│   ├── delete.go              # Delete command and flags
│   ├── delete_interactive.go  # Interactive delete flow
│   ├── delete_noninteractive.go # Non-interactive delete
│   ├── delete_git.go          # Git operations for delete
│   ├── delete_types.go        # Delete type definitions
│   ├── delete_xr.go           # XR deletion in *-xrs directories (delete-claim/<name> PR)
│   ├── apiauth.go             # Shared --api-token / --api-oidc-* flags, API client factory
│   ├── encrypt.go             # Encrypt command and flags
│   ├── encrypt_interactive.go # Interactive encrypt flow (SOPS)
│   ├── encrypt_noninteractive.go # Non-interactive encrypt
│   ├── encrypt_git.go         # Git operations for encrypt
│   ├── encrypt_types.go       # Encrypt type definitions
│   ├── list.go                # List command
│   ├── version.go             # Version command
│   └── logo.go                # ASCII logo rendering
├── internal/
│   ├── templates/
│   │   ├── types.go           # API data models
│   │   ├── client.go          # HTTP client for claim-machinery API
│   │   └── client_test.go     # Client unit tests
│   ├── apiauth/
│   │   ├── apiauth.go         # Optional bearer-token / OIDC client-credentials auth
│   │   └── apiauth_test.go    # Auth tests (fake token endpoint)
│   ├── gitops/
│   │   ├── operations.go      # Git operations (clone, add, commit, push)
│   │   ├── operations_test.go # Git operations tests
│   │   ├── branch.go          # Branch management
│   │   ├── branch_test.go     # Branch management tests
│   │   ├── auth.go            # Credential resolution
│   │   ├── auth_test.go       # Auth tests
│   │   ├── pr.go              # Pull request creation via the GitHub API (go-github)
│   │   └── pr_test.go         # PR creation tests
│   ├── sops/
│   │   ├── sops.go            # SOPS binary interaction (check, encrypt)
│   │   ├── secret.go          # K8s Secret YAML generation
│   │   └── sops_test.go       # SOPS unit tests
│   ├── registry/
│   │   ├── registry.go        # Registry CRUD operations
│   │   └── types.go           # Registry type definitions
│   ├── kustomize/
│   │   └── kustomize.go       # Kustomization.yaml operations
│   └── params/
│       ├── types.go           # Parameter types
│       ├── file.go            # File parsing logic
│       └── file_test.go       # Parameter parsing tests
├── tests/
│   ├── params.yaml            # Example params file for testing
│   ├── test_gitops.sh         # GitOps integration tests (shell)
│   └── integration/
│       └── render_test.go     # End-to-end integration tests
├── examples/
│   ├── basic-params.yaml      # Basic single-template example
│   ├── multi-template.yaml    # Multi-template batch rendering
│   └── gitops-workflow.yaml   # GitOps workflow example
├── docs/                      # Detailed documentation
├── go.mod                     # Go module definition
├── Taskfile.yaml              # Task automation
├── catalog-info.yaml          # Backstage component definition
└── README.md                  # This file
```

## License

See [LICENSE](LICENSE) for details.

## Author

patrick hermann
