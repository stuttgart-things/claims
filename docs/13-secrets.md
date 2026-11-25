# claims secret: SOPS in git, Vault at runtime

`claims secret` (claims#122) manages declared secrets following the target picture of
crossplane-configurations#572:

- **SOPS in git is the master of declared secrets**: PATs, external credentials, app
  passwords, ZITADEL client secrets. **Derived** secrets (PKI, AppRole `secret_id`s,
  tokens, anything Vault issues itself) never go into git.
- **Vault (with ESO) is the runtime.** Crossplane reconciles SOPS → Vault later
  (Flux decrypts → Kubernetes Secret → `VaultSecretSet`). One direction per path; Vault →
  SOPS is always a PR (`claims secret sync vault-to-sops --create-pr`), never a controller.

It does not need claim-machinery-api.

## Layout: the file path is the Vault path

```
secrets/<lab>/<mount>/<path>.enc.yaml   <=>   <mount>/<path>  (KV v2, in the Vault of <lab>)

secrets/labul/kv/machinery-dev/argocd-oidc.enc.yaml   <=>   kv/machinery-dev/argocd-oidc
```

No mapping configuration. Only files below `secrets/<lab>/<mount>/` belong to the layout:
the flat bootstrap credentials `secrets/*.enc.yaml` of the fleet repository stay as they
are, and claims leaves every file outside the layout alone (directory walks skip them
with a note). `claims secret path` converts both ways, and every command
accepts either form (a Vault path needs the lab: `--lab`, `$CLAIMS_LAB`, or
`claims secret vault use <lab>`).

Each file is a Kubernetes Secret: `kind: Secret`, `type: Opaque`, every value under
`stringData`, one key per Vault key. Files claims creates look like this before
encryption (only `stringData` is encrypted):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: labul.kv.machinery-dev.argocd-oidc
  namespace: vault-sync
  annotations:
    claims.stuttgart-things.com/lab: labul
    claims.stuttgart-things.com/vault-path: kv/machinery-dev/argocd-oidc
type: Opaque
stringData:
  clientID: …
  clientSecret: …
```

A file with `data:` (base64) is read too; claims writes it back as `stringData`.

### metadata.name

Derived from the path, deterministic and collision-free, always a valid DNS-1123
subdomain (≤ 253 characters):

1. **Clean form**: if every segment of `<lab>/<mount>/<path>` is a DNS-1123 label
   (lowercase letters, digits, inner `-`, ≤ 63 characters) and contains no `--`, the
   name is the segments joined by `.`:
   `labul/kv/machinery-dev/argocd-oidc` → `labul.kv.machinery-dev.argocd-oidc`.
2. **Hashed form** for everything else (uppercase, `_`, `.`, `--`, other characters,
   too long): the lowercased path, every run of other characters turned into one `-`,
   segments joined by `.`, then `--` and 10 hex digits of `sha256("<lab>/<mount>/<path>")`:
   `labul/kv/App_Creds` → `labul.kv.app-creds--<10 hex>`.

Why this rule: the lab is part of the name because one management cluster may hold the
Secrets of both labs in the same namespace. `.` as separator keeps the clean form
injective (a clean segment cannot contain `.`). A clean name never contains `--` and a
hashed name always does, so the two forms never collide; hashed names carry 40 bits of
the exact path. A pure slug (`/` → `-`) would map `kv/a-b` and `kv/a/b` to the same name.

The namespace is `vault-sync` (phase 1), `--namespace` for files claims creates;
existing files keep theirs.

Phase 1 reads the files through the sops-secrets-operator (`SopsSecretManifest`,
`sops.stuttgart-things.com/v1alpha2`), which sets the target Secret's name and namespace
itself (`target.nameOverride` / `target.namespace`, crossplane-configurations#572
"Decisions"). The name and namespace in the file are therefore only a deterministic
default, not what the cluster ends up with.

## .sops.yaml: one recipient per lab plus the recovery recipient

Recipients come only from the **root `.sops.yaml` of the fleet repository**. claims runs
sops in the repository root with `--filename-override <path>`, so the first creation rule
whose `path_regex` matches decides. claims reads the rules itself first and stops when
none matches; it never guesses a recipient.

```yaml
creation_rules:
  - path_regex: ^secrets/labul/.*\.enc\.yaml$
    encrypted_regex: ^(data|stringData)$
    age: >-
      <labul recipient>,
      <offline recovery recipient>
  - path_regex: ^secrets/labda/.*\.enc\.yaml$
    encrypted_regex: ^(data|stringData)$
    age: >-
      <labda recipient>,
      <offline recovery recipient>
```

A complete example is in [`examples/fleet-secrets/.sops.yaml`](../examples/fleet-secrets/.sops.yaml)
(placeholders; it belongs into stuttgart-things/stuttgart-things, not here).

After every encryption claims checks that every value under `data`/`stringData` came out
as `ENC[…]` before the file is written, so a rule with a wrong `encrypted_regex` can never
put plaintext into git.

`claims doctor --lab <lab>` checks the rule; the recovery recipient is
`--recovery-recipient` / `$CLAIMS_SOPS_RECOVERY_RECIPIENT`, else the recipient shared by
all `secrets/` rules.

## Derived secrets: secrets/&lt;lab&gt;/.derived

A plain list of Vault path globs (`<mount>/<path>`; `*` within a segment, `**` across,
`?` one character; a pattern without wildcards also covers everything below it; `#`
comments). `sync vault-to-sops` skips them (naming one explicitly is an error), `diff`
does not count them as drift. `--exclude` adds patterns per run. Example:
[`examples/fleet-secrets/secrets/labul/.derived`](../examples/fleet-secrets/secrets/labul/.derived).

## Vault

```bash
claims secret vault add labul https://vault.labul.example.com --ca-file ~/lab-ca.pem
claims secret vault add labda https://vault.labda.example.com --auth approle
claims secret vault use labul
claims secret vault list
```

stored in `~/.config/claims/config.yaml` (`$CLAIMS_CONFIG_FILE`) under `vaults:`.
Resolution: `--vault-addr` > the lab's entry > `$VAULT_ADDR` (with a warning). KV v2 only.

Auth (OIDC is planned, see the decision on claims#122):

| method | from |
|---|---|
| token | `--vault-token`, `$VAULT_TOKEN`, `~/.vault-token` (`vault login`) |
| AppRole (CI) | `$VAULT_ROLE_ID` + `$VAULT_SECRET_ID`, mount `approle` (`--vault-approle-mount`) |

## Commands

| Command | What |
|---|---|
| `claims secret path <vault-path\|file\|dir>` | the counterpart; `-o yaml\|json` with lab, file, Secret name |
| `claims secret sops view <target>` | keys, length, sha256 prefix; values only with `--reveal` (`--reveal --key k`: the bare value) |
| `claims secret sops set <target> k=v k=@file k` | set keys (value, file, stdin/hidden prompt); `--generate --length --charset` creates values in SOPS (kept if present unless `--overwrite`); creates the file |
| `claims secret sops unset <target> k…` | remove keys |
| `claims secret sops rotate <target\|dir>…` | `sops updatekeys` + `sops rotate` (new data key), values checked unchanged |
| `claims secret diff <target\|dir\|prefix/>` | hashes only; per path `=` / `~` / `+git` / `+vault`; exit **5** on drift; `-o json` |
| `claims secret sync vault-to-sops <…>` | migration: Vault entries (one or a tree) → SOPS files, plan first, conflicts stop, `--create-pr` |
| `claims secret sync sops-to-vault <…>` | bootstrap only: SOPS files → Vault, conflicts need `--force` |
| `claims doctor --lab/--secret` | sops ≥ 3.9, age key, rule + recovery recipient, can your key decrypt, Vault login/TTL/capabilities |

Writing commands (`set`, `unset`, `rotate`, `sync vault-to-sops`) change the working tree;
with `--git-branch` / `--create-pr` they commit on a branch (default
`secret/<command>-<lab>-<time>`), push and open a PR, as render and delete do. Commit
messages and PR bodies name paths and keys, never values or hashes.

### Examples

```bash
# Where does this Vault entry live in git?
claims secret path kv/machinery-dev/argocd-oidc --lab labul

# A generated password, born in SOPS
claims secret sops set kv/machinery-dev/db --lab labul password --generate --length 40 --charset symbols

# A PAT from a password manager, without the shell history
pass show github/pat | claims secret sops set secrets/labul/kv/ci/github.enc.yaml token

# Migrate everything below kv/machinery-dev into git, as a PR
claims secret sync vault-to-sops kv/machinery-dev/ --lab labul --dry-run
claims secret sync vault-to-sops kv/machinery-dev/ --lab labul --create-pr

# Scheduled drift check (exit 5 on drift)
claims secret diff secrets/labul/kv/ -o json

# A recipient was added to .sops.yaml
claims secret sops rotate secrets/labul/ --create-pr
```

## Safety rules

- No plaintext on disk: sops decrypts to a pipe and encrypts from stdin
  (`/dev/stdin` + `--filename-override`); files are written via a temp file + rename of
  the ciphertext only.
- No values in output, errors, commit messages or PR texts; sops' error text is scrubbed
  of the values being written; YAML errors of decrypted content are not passed on.
- Read-back after every write: a SOPS file is decrypted and compared, a Vault entry is
  read and compared.
- Exit codes: 0 ok, 1 error, 2 usage, 5 drift (`diff`).

## Not in v1

- Vault OIDC login and a per-Vault token cache (token and AppRole only).
- `claims secret vault ls/get/put/patch` (claims#122 table); `diff`/`sync` cover the
  migration path.
- Interactive (huh) browsing of Vault paths; every step is a flag.
- `--merge` for the secret PRs, `ref+vault://` / `ref+sops://` references.
- Engines other than KV v2.
