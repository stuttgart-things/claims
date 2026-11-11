# Endpoints, Preflight and `claims doctor`

claims#116 (named endpoints) and claims#114 (fail early).

## Named endpoints

`~/.config/claims/config.yaml` (override with `CLAIMS_CONFIG_FILE`) keeps named
claim-machinery-api endpoints, like the contexts of a kubeconfig. Nothing is
pre-configured; the file holds what you add:

```yaml
current: labda
endpoints:
  labda:
    url: https://claim-machinery-api.machinery.4sthings.tiab.ssc.sva.de
    issuer: https://zitadel.sthings-infra.4sthings.tiab.ssc.sva.de   # for claims login
    client-id: <id>@claims                                            # native ZITADEL app
    # ca-file: /path/to/lab-ca.pem                                    # optional, issuer with a private CA
  labul:
    url: https://claim-machinery-api.machinery-labul.sthings-vsphere.labul.sva.de
```

```bash
claims endpoint add labda https://claim-machinery-api.machinery.4sthings.tiab.ssc.sva.de \
  --issuer https://zitadel.sthings-infra.4sthings.tiab.ssc.sva.de --client-id <id>@claims --use
claims endpoint add labul https://claim-machinery-api.machinery-labul.sthings-vsphere.labul.sva.de
claims endpoint list              # * = current
claims endpoint use labul
claims endpoint current           # the API a command would use, and where that came from
claims endpoint remove labul
```

The client ID is the one of the lab's native `claims-cli` ZITADEL application
(see [10-login.md](10-login.md)); it is not in this repository.

### Which API a command uses

1. `-a` / `--api-url`
2. `$CLAIM_API_URL` (colon-separated list allowed for `render`)
3. `--endpoint <name>` / `$CLAIM_ENDPOINT`
4. the current endpoint (`claims endpoint use`)

`$CLAIM_API_URL` wins over `--endpoint`; claims warns when it ignores an
endpoint for that reason. A URL given with `-a` or `$CLAIM_API_URL` that
matches a configured endpoint is shown with the endpoint's name and uses its
login settings.

With none of them there is **no fallback to `http://localhost:8080`**
(it once answered `404 page not found` for a forgotten export): an interactive
command shows a picker of the configured endpoints (or asks for a URL), a
non-interactive one fails and names the fix. A local API is
`-a http://localhost:8080`.

### Login per endpoint

`claims login --endpoint labda` takes issuer, client ID and CA file from the
endpoint. Flags (`--issuer`, `--client-id`, `--ca-file`) and
`CLAIM_API_LOGIN_*` still win; the previous login for the same URL fills in
last. `claims whoami` and `claims logout` take `--endpoint` as well.

## Preflight

Before `render`, `encrypt` and `delete` render, branch or commit anything,
they check what they will need and list **every** problem at once, each with
the flag or variable that fixes it, then exit 1:

| Check | What | Fix named |
|-------|------|-----------|
| API endpoint | an API is chosen (see above) | `-a`, `--endpoint`, `CLAIM_API_URL`, `claims endpoint add … --use` |
| API reachable | `GET /api/v1/claim-templates` answers 200 with a template list | the right `--endpoint` / `-a` |
| API auth | the credentials claims would send are usable (cached login not expired or refreshable, `CLAIM_API_TOKEN`, `CLAIM_API_OIDC_*`); a 401/403 without any | `claims login --endpoint <name>` |
| git repo / branch | the output directory is a checkout; `--git-create-branch` does not hit an existing branch, `--git-branch` without it names one that exists | `git branch -D …`, another `--git-branch`, `--git-create-branch` |
| git token | `--git-token`, `GIT_TOKEN`, `GITHUB_TOKEN`, else `gh auth token` | `export GITHUB_TOKEN=$(gh auth token)` / `gh auth login` |
| git remote | the remote accepts the token (`git ls-remote`) | token with `contents: write` |
| GitHub PR | with `--create-pr` / `--merge`: the token may push to the GitHub repository | token with `contents`/`pull-requests: write` |

The git checks run with `--git-push` / `--create-pr` (and in the interactive
flows right after the git options are chosen). `--dry-run` and `--stdout`
skip them. `--skip-preflight` skips all checks.

```text
$ claims render --non-interactive -t vm -f params.yaml --create-pr
preflight: 2 problem(s), nothing was written:
  FAIL  API endpoint  no claim-machinery-api endpoint chosen (there is no localhost default)
                      fix: pass -a <url> or --endpoint <name>, set CLAIM_API_URL or CLAIM_ENDPOINT, or configure one: claims endpoint add <name> <url> --use
  FAIL  git token     no token to push with (git@github.com:o/r.git is SSH; claims pushes over HTTPS with a token, the SSH agent is not used)
                      fix: pass --git-token, set GIT_TOKEN or GITHUB_TOKEN (e.g. export GITHUB_TOKEN=$(gh auth token)), or log in with `gh auth login`
(--skip-preflight runs anyway; `claims doctor` runs the checks alone)
```

### SSH remotes

claims pushes and clones with go-git and a token over HTTPS. An SSH remote
(`git@github.com:o/r.git`, `ssh://…`) is pushed to at its HTTPS URL
(`https://github.com/o/r.git`); the SSH agent is not used. Before, the token
was offered to the SSH transport and the push failed with
`pushing: invalid auth method` after the commit.

`--git-user` / `GIT_USER` is optional: with only a token, claims
authenticates as `x-access-token` and commits as `claims-cli`.

### When the push or PR fails anyway

claims does **not** roll back the local branch and commit: they hold what was
rendered (generated passwords included), and a reset would lose it. The error
says where the work is and how to finish or undo it:

```text
pushing: …
Nothing was pushed. The commit is on the local branch vm-claims-auth in /home/me/fleet.
  retry:  git -C /home/me/fleet push -u origin vm-claims-auth
          then: gh pr create --head vm-claims-auth --base main   (if a PR was asked for)
  undo:   git -C /home/me/fleet switch main && git -C /home/me/fleet branch -D vm-claims-auth
```

A failed PR after a successful push prints the `gh pr create` command. With
`--git-repo-url` the temporary clone is removed; run claims again.

## claims doctor

The same checks on their own, read-only (no file, branch or commit), every
result printed, exit 1 when one fails:

```bash
claims doctor                                    # current endpoint, checkout in .
claims doctor --endpoint labul
claims doctor -a https://claims.example.com --dir ~/fleet --git-remote origin
claims doctor --no-git                           # API only
claims doctor --no-api --no-github               # git push only
```

```text
  ok    API endpoint  labda (https://claim-machinery-api.machinery.4sthings.tiab.ssc.sva.de) (from current endpoint …)
  ok    API reachable https://claim-machinery-api.machinery.4sthings.tiab.ssc.sva.de (3 templates)
  ok    API auth      login
  ok    git repo      /home/me/fleet
  ok    git token     found
  ok    git remote    git@github.com:o/r.git is SSH: claims pushes over HTTPS with the token (https://github.com/o/r.git)
  ok    git remote    https://github.com/o/r.git accepts the token
  ok    GitHub PR     o/r

All checks passed.
```

`claims change` talks only to the GitHub API (no push) and has no preflight of
its own; its token comes from the same sources, and `claims doctor` shows
whether that token can see and push to the repository (the `actions: write`
permission `change` also needs is not checked).
