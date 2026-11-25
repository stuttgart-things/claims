package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/secretpath"
	"github.com/stuttgart-things/claims/internal/sops"
	"github.com/stuttgart-things/claims/internal/vault"
)

// claims secret sync (crossplane-configurations#572): one direction per
// path. vault-to-sops is the migration of what today lives only in Vault
// into git, always as a PR; sops-to-vault is the bootstrap of a fresh Vault
// until Crossplane reconciles SOPS -> Vault.

var secretSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Copy secrets between Vault and SOPS (vault-to-sops: migration via PR; sops-to-vault: bootstrap)",
}

var secretSyncFlags struct {
	exclude   []string
	overwrite bool
	dryRun    bool
	git       secretGit
}

var secretSyncV2SCmd = &cobra.Command{
	Use:     "vault-to-sops <vault-path|prefix/|file|dir>...",
	Aliases: []string{"vault→sops", "vault2sops"},
	Short:   "Write Vault entries (one or a tree) to their SOPS files, optionally as a PR",
	Long: `Reads KV v2 entries from the lab's Vault and writes each to its 1:1 SOPS file
secrets/<lab>/<mount>/<path>.enc.yaml (a Kubernetes Secret, stringData, one key
per Vault key), encrypted for the creation rule of .sops.yaml, read back after
writing. With --create-pr the files go onto a branch and into a pull request
(paths and key names only, no values, no hashes).

Per path: create (no file yet), unchanged (the file holds the same values),
conflict (the file differs; nothing is written unless --overwrite).

Derived secrets never go into git: paths listed in secrets/<lab>/.derived or
matching --exclude are skipped. Entries whose values are not all strings, or
whose keys cannot be Secret keys, are skipped and listed.`,
	Example: `  claims secret sync vault-to-sops kv/machinery-dev/argocd-oidc --lab labul
  claims secret sync vault-to-sops kv/ --lab labul --exclude 'kv/kubeconfigs/**' --dry-run
  claims secret sync vault-to-sops kv/machinery-dev/ --lab labul --create-pr`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSyncVaultToSops(cmd.Context(), args, os.Stdout))
	},
}

var secretSyncS2VFlags struct {
	exclude []string
	force   bool
	dryRun  bool
}

var secretSyncS2VCmd = &cobra.Command{
	Use:     "sops-to-vault <file|vault-path|dir>...",
	Aliases: []string{"sops→vault", "sops2vault"},
	Short:   "Write SOPS files to their Vault entries (bootstrap; Crossplane does this later)",
	Long: `Decrypts SOPS files and writes each to its 1:1 Vault path (KV v2, a new
version with exactly the file's keys), then reads it back.

Per path: create (no entry yet), unchanged, conflict (the entry differs;
nothing is written unless --force). Meant for bootstrapping a fresh Vault;
afterwards Crossplane reconciles SOPS -> Vault and nobody writes declared
paths by hand.`,
	Example: `  claims secret sync sops-to-vault secrets/labul/kv/ --dry-run
  claims secret sync sops-to-vault secrets/labul/kv/app/db.enc.yaml`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSyncSopsToVault(cmd.Context(), args, os.Stdout))
	},
}

func init() {
	f := secretSyncV2SCmd.Flags()
	f.StringSliceVar(&secretSyncFlags.exclude, "exclude", nil, "Skip Vault paths matching these globs (mount/path; * ** ?), in addition to secrets/<lab>/.derived")
	f.BoolVar(&secretSyncFlags.overwrite, "overwrite", false, "Replace the values of existing files that differ from Vault")
	f.BoolVar(&secretSyncFlags.dryRun, "dry-run", false, "Show the plan, write nothing")
	addNamespaceFlag(secretSyncV2SCmd)
	addSecretGitFlags(secretSyncV2SCmd, &secretSyncFlags.git)

	g := secretSyncS2VCmd.Flags()
	g.StringSliceVar(&secretSyncS2VFlags.exclude, "exclude", nil, "Skip Vault paths matching these globs, in addition to secrets/<lab>/.derived")
	g.BoolVar(&secretSyncS2VFlags.force, "force", false, "Overwrite Vault entries that differ from the file")
	g.BoolVar(&secretSyncS2VFlags.dryRun, "dry-run", false, "Show the plan, write nothing")

	secretSyncCmd.AddCommand(secretSyncV2SCmd, secretSyncS2VCmd)
	secretCmd.AddCommand(secretSyncCmd)
}

// Plan actions.
const (
	actCreate    = "create"
	actUpdate    = "update"
	actUnchanged = "unchanged"
	actConflict  = "conflict"
	actSkip      = "skip"
)

type syncItem struct {
	target secretTarget
	action string
	reason string // skip / conflict
	keys   []string
	values map[string]string // the source values (memory only)
	dest   *sops.Manifest    // vault-to-sops: the manifest to write
}

// syncScope resolves the arguments of a sync to refs of one lab. fromVault
// lists trees in Vault, else in git.
func syncScope(ctx context.Context, root string, args []string, fromVault bool, vc func(lab string) (*vault.Client, error)) (lab string, refs []secretpath.Ref, explicit map[string]bool, err error) {
	explicit = map[string]bool{}
	seen := map[string]bool{}
	add := func(r secretpath.Ref) error {
		if lab == "" {
			lab = r.Lab
		} else if r.Lab != lab {
			return usagef("one lab per sync: %s and %s", lab, r.Lab)
		}
		if !seen[r.VaultPath()] {
			seen[r.VaultPath()] = true
			refs = append(refs, r)
		}
		return nil
	}
	for _, a := range args {
		if isTreeArg(root, a) {
			tree, err := resolveSecretTree(root, a)
			if err != nil {
				return "", nil, nil, err
			}
			var found []secretpath.Ref
			if fromVault {
				c, err := vc(tree.Lab)
				if err != nil {
					return "", nil, nil, err
				}
				paths, err := c.List(ctx, tree.Mount, tree.Prefix)
				if err != nil {
					return "", nil, nil, err
				}
				for _, p := range paths {
					r, err := secretpath.ParseVault(tree.Lab, tree.Mount+"/"+p)
					if err != nil {
						warnf(fmt.Sprintf("skipping %s/%s: %v", tree.Mount, p, err))
						continue
					}
					found = append(found, r)
				}
			} else if found, err = gitSecrets(root, tree); err != nil {
				return "", nil, nil, err
			}
			if len(found) == 0 {
				warnf("nothing below " + tree.VaultPath())
			}
			for _, r := range found {
				if err := add(r); err != nil {
					return "", nil, nil, err
				}
			}
			continue
		}
		t, err := resolveSecretTarget(root, a)
		if err != nil {
			return "", nil, nil, err
		}
		explicit[t.Ref.VaultPath()] = true
		if err := add(t.Ref); err != nil {
			return "", nil, nil, err
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].VaultPath() < refs[j].VaultPath() })
	return lab, refs, explicit, nil
}

// vaultClientCache logs in once per lab.
func vaultClientCache(ctx context.Context) func(string) (*vault.Client, error) {
	clients := map[string]*vault.Client{}
	return func(lab string) (*vault.Client, error) {
		if c, ok := clients[lab]; ok {
			return c, nil
		}
		c, err := newVaultClient(ctx, lab)
		if err != nil {
			return nil, err
		}
		clients[lab] = c
		return c, nil
	}
}

// excludeItems splits off the derived paths; naming one explicitly is an error.
func excludeItems(refs []secretpath.Ref, explicit map[string]bool, ex *secretpath.Excluder) ([]secretpath.Ref, []string, error) {
	var keep []secretpath.Ref
	var skipped []string
	for _, r := range refs {
		if pat, ok := ex.Match(r.VaultPath()); ok {
			if explicit[r.VaultPath()] {
				return nil, nil, fmt.Errorf("%s is a derived secret (%s): derived secrets never go into git", r.VaultPath(), pat)
			}
			skipped = append(skipped, r.VaultPath())
			continue
		}
		keep = append(keep, r)
	}
	return keep, skipped, nil
}

func validKeys(values map[string]string) error {
	for _, k := range sops.Keys(values) {
		if err := sops.ValidKey(k); err != nil {
			return err
		}
	}
	return nil
}

func printPlan(out io.Writer, items []syncItem, excluded []string, toVault bool) map[string]int {
	counts := map[string]int{}
	for _, it := range items {
		counts[it.action]++
		dest := it.target.Rel
		if toVault {
			dest = it.target.Ref.String()
		}
		line := fmt.Sprintf("  %-9s %s", it.action, it.target.Ref.VaultPath())
		if it.action != actSkip {
			line += "  ->  " + dest
		}
		if len(it.keys) > 0 && (it.action == actCreate || it.action == actUpdate || it.action == actConflict) {
			line += "  [" + strings.Join(it.keys, ", ") + "]"
		}
		if it.reason != "" {
			line += "  (" + it.reason + ")"
		}
		_, _ = fmt.Fprintln(out, line)
	}
	if len(excluded) > 0 {
		_, _ = fmt.Fprintf(out, "  %d derived path(s) excluded\n", len(excluded))
	}
	return counts
}

func runSyncVaultToSops(ctx context.Context, args []string, out io.Writer) error {
	ctx = ctxOrBackground(ctx)
	root, err := secretRepoRoot()
	if err != nil {
		return err
	}
	if err := checkSOPSBinary(ctx); err != nil {
		return err
	}
	vcFor := vaultClientCache(ctx)
	lab, refs, explicit, err := syncScope(ctx, root, args, true, vcFor)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return errors.New("no Vault entries to sync")
	}
	ex, err := secretExcluder(root, lab, secretSyncFlags.exclude)
	if err != nil {
		return err
	}
	refs, excluded, err := excludeItems(refs, explicit, ex)
	if err != nil {
		return err
	}
	vc, err := vcFor(lab)
	if err != nil {
		return err
	}
	if !secretSyncFlags.dryRun {
		if err := secretSyncFlags.git.preflight(ctx, root); err != nil {
			return err
		}
	}

	var items []syncItem
	for _, r := range refs {
		t := targetFor(root, r)
		it := syncItem{target: t}
		if _, err := sopsRule(t); err != nil {
			return err
		}
		vals, err := vc.Get(ctx, r.Mount, r.Path)
		if err != nil {
			skippable := errors.Is(err, vault.ErrNotStrings) || (errors.Is(err, vault.ErrNotFound) && !explicit[r.VaultPath()])
			if !skippable {
				return err
			}
			it.action, it.reason = actSkip, strings.TrimPrefix(err.Error(), r.VaultPath()+": ")
			items = append(items, it)
			continue
		}
		if err := validKeys(vals); err != nil {
			it.action, it.reason = actSkip, err.Error()
			items = append(items, it)
			continue
		}
		if len(vals) == 0 {
			it.action, it.reason = actSkip, "no keys"
			items = append(items, it)
			continue
		}
		it.values, it.keys = vals, sops.Keys(vals)
		if !t.exists() {
			it.action = actCreate
			it.dest = newSecretManifest(r, secretOpts.namespace)
		} else {
			m, _, err := readSecretFile(ctx, t)
			if err != nil {
				return err
			}
			switch {
			case sops.SameValues(m.Values, vals):
				it.action = actUnchanged
			case secretSyncFlags.overwrite:
				it.action, it.dest = actUpdate, m
				it.reason = keyChangeSummary(compareValues(m.Values, vals))
			default:
				it.action = actConflict
				it.reason = keyChangeSummary(compareValues(m.Values, vals)) + "; --overwrite replaces the file's values"
			}
		}
		items = append(items, it)
	}

	_, _ = fmt.Fprintf(out, "Vault %s (lab %s) -> SOPS:\n", vc.Addr, lab)
	counts := printPlan(out, items, excluded, false)
	if counts[actConflict] > 0 {
		return fmt.Errorf("%s: the files differ from Vault; nothing written (check with claims secret diff, then --overwrite)", plural(counts[actConflict], "conflict"))
	}
	if secretSyncFlags.dryRun {
		_, _ = fmt.Fprintln(out, "Dry run: nothing written.")
		return nil
	}

	var files, written, skipped []string
	for _, it := range items {
		switch it.action {
		case actCreate, actUpdate:
			it.dest.Values = it.values
			if err := writeSecretFile(ctx, it.target, it.dest); err != nil {
				return fmt.Errorf("%w\n%s written before the error (still in the working tree)", err, plural(len(files), "file"))
			}
			files = append(files, it.target.Abs)
			written = append(written, fmt.Sprintf("- `%s` → `%s` (%s: %s)", it.target.Ref.VaultPath(), it.target.Rel, it.action, strings.Join(it.keys, ", ")))
		case actSkip:
			skipped = append(skipped, fmt.Sprintf("- `%s`: %s", it.target.Ref.VaultPath(), it.reason))
		}
	}
	_, _ = fmt.Fprintln(out, successStyle.Render(fmt.Sprintf("Wrote %s (read-back ok), %d unchanged, %d skipped", plural(len(files), "file"), counts[actUnchanged], counts[actSkip])))
	if len(files) == 0 {
		return nil
	}
	body := fmt.Sprintf("Imports Vault KV v2 entries of lab **%s** into SOPS files at their 1:1 paths "+
		"(`secrets/<lab>/<mount>/<path>.enc.yaml` ⇔ `<mount>/<path>`), encrypted for the creation rules of `.sops.yaml`.\n\n"+
		"### Written\n\n%s\n", lab, strings.Join(written, "\n"))
	if len(skipped) > 0 {
		body += "\n### Not migrated\n\n" + strings.Join(skipped, "\n") + "\n"
	}
	if len(excluded) > 0 {
		body += fmt.Sprintf("\n%d derived path(s) excluded (secrets/%s/.derived, --exclude).\n", len(excluded), lab)
	}
	return secretSyncFlags.git.commit(root, secretChange{
		Command: "sync-vault-to-sops", Lab: lab, Files: files,
		Subject: fmt.Sprintf("secret: import %s of %s from Vault into SOPS", plural(len(files), "secret"), lab),
		Body:    body,
	}, out)
}

// keyChangeSummary names the keys that differ, without values.
func keyChangeSummary(diffs []keyDiff) string {
	var parts []string
	for _, d := range diffs {
		parts = append(parts, d.Key+" "+d.State)
	}
	return strings.Join(parts, ", ")
}

func runSyncSopsToVault(ctx context.Context, args []string, out io.Writer) error {
	ctx = ctxOrBackground(ctx)
	root, err := secretRepoRoot()
	if err != nil {
		return err
	}
	if err := checkSOPSBinary(ctx); err != nil {
		return err
	}
	vcFor := vaultClientCache(ctx)
	lab, refs, explicit, err := syncScope(ctx, root, args, false, vcFor)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return errors.New("no SOPS files to sync")
	}
	ex, err := secretExcluder(root, lab, secretSyncS2VFlags.exclude)
	if err != nil {
		return err
	}
	refs, excluded, err := excludeItems(refs, explicit, ex)
	if err != nil {
		return err
	}
	vc, err := vcFor(lab)
	if err != nil {
		return err
	}
	var items []syncItem
	for _, r := range refs {
		t := targetFor(root, r)
		if !t.exists() {
			return fmt.Errorf("%s does not exist", t.Rel)
		}
		m, _, err := readSecretFile(ctx, t)
		if err != nil {
			return err
		}
		it := syncItem{target: t, values: m.Values, keys: sops.Keys(m.Values)}
		cur, err := vc.Get(ctx, r.Mount, r.Path)
		switch {
		case errors.Is(err, vault.ErrNotFound):
			it.action = actCreate
		case err != nil:
			return err
		case sops.SameValues(cur, m.Values):
			it.action = actUnchanged
		case secretSyncS2VFlags.force:
			it.action, it.reason = actUpdate, keyChangeSummary(compareValues(m.Values, cur))
		default:
			it.action = actConflict
			it.reason = keyChangeSummary(compareValues(m.Values, cur)) + "; --force overwrites Vault"
		}
		items = append(items, it)
	}
	_, _ = fmt.Fprintf(out, "SOPS -> Vault %s (lab %s):\n", vc.Addr, lab)
	counts := printPlan(out, items, excluded, true)
	if counts[actConflict] > 0 {
		return fmt.Errorf("%s: Vault differs from the files; nothing written (--force overwrites)", plural(counts[actConflict], "conflict"))
	}
	if secretSyncS2VFlags.dryRun {
		_, _ = fmt.Fprintln(out, "Dry run: nothing written.")
		return nil
	}
	n := 0
	for _, it := range items {
		if it.action != actCreate && it.action != actUpdate {
			continue
		}
		if err := vc.Put(ctx, it.target.Ref.Mount, it.target.Ref.Path, it.values); err != nil {
			return fmt.Errorf("%w\n%d entries written before the error", err, n)
		}
		n++
	}
	_, _ = fmt.Fprintln(out, successStyle.Render(fmt.Sprintf("Wrote %d Vault entries (read-back ok), %d unchanged", n, counts[actUnchanged])))
	return nil
}
