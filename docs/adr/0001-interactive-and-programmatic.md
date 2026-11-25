# 0001: Interactive for people, programmatic for scripts and agents

- Status: accepted
- Date: 2026-11-18

## Context

claims started as a terminal UI: pick a template, fill a form, review, open a PR. Non-interactive mode was added for CI (`--non-interactive`, `-p`, `-f`), command by command, so it is uneven: some commands detect the terminal, some need the flag, a few have `-o json`, and some answers exist only as tables or prose.

Two kinds of non-human callers now use claims: CI pipelines and AI personas (stuttgart-things/ai-personas, ADR 0001 there). A persona runs claims through `claude -p`, without a terminal, and learns the CLI from `--help` and error messages, like a new colleague. In the first pilot it could not order or even list the templates with claims, and fell back to a hand-written client.

## Decision

Every command has two faces, and both are first-class:

1. **Interactive** when stdin is a terminal and nothing forces otherwise: forms, pickers, confirmations, colour. This is the human path and stays the default at a terminal.
2. **Programmatic** when stdin is not a terminal, or with `--non-interactive`:
   - **never prompts.** A missing value is an error that names the flag (or env var) that supplies it, e.g. `--cluster is required (list them: claims clusters)`.
   - **everything a form asks is a flag**, and every list a picker offers is a command (`claims templates`, `claims clusters`, `claims orders`).
   - **`-o json`** on every command that returns data; the JSON shape is documented and stable (fields are added, not renamed). The default stays a human table or text.
   - **exit codes:** 0 ok, 1 error, 2 usage error (missing/invalid flag), 3 denied by the server (401/403), 4 not found, 5 drift (`claims secret diff`). Errors go to stderr, data to stdout.
   - **server messages pass through:** a 403 shows the server's reason (template `orderRoles`, foreign owner), not just the status.
   - **`--help` is the manual:** each command's help says what it does, which flags are required in programmatic mode, one example, and where the result goes (PR, order ID, file).
3. **Same auth on both sides:** cached `claims login`, `--api-token` / `CLAIM_API_TOKEN`, or client credentials (`--api-oidc-*`).

## Consequences

- New commands are written with both faces from the start; a PR that adds a prompt without its flag is incomplete.
- Existing commands are aligned step by step (exit codes, `-o json`, help texts), starting with the order path: `claims templates` (#125), `claims order` / `claims orders` (#124, ADR 0002).
- Tests cover the programmatic face without a terminal (no TTY in `go test`), including the "missing flag" errors.
