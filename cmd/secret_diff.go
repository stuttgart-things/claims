package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/secretpath"
	"github.com/stuttgart-things/claims/internal/sops"
	"github.com/stuttgart-things/claims/internal/vault"
)

// claims secret diff: SOPS file vs. Vault entry, by sha256 only. Over a tree
// the result per path is equal / different / only in git / only in Vault;
// drift exits 5, for a scheduled job (crossplane-configurations#572).

// exitCodeDrift is the exit code of diff when git and Vault differ.
const exitCodeDrift = 5

// driftError is a diff that found drift (exit 5).
type driftError struct{ n int }

func (e *driftError) Error() string {
	return fmt.Sprintf("drift between git and Vault: %s", plural(e.n, "path"))
}

var secretDiffFlags struct {
	exclude []string
	output  string
	verbose bool
}

var secretDiffCmd = &cobra.Command{
	Use:   "diff <file|vault-path|dir|prefix/>",
	Short: "Compare SOPS files with their Vault entries (hashes only; exit 5 on drift)",
	Long: `Compares the keys of a SOPS file with the Vault entry at the same path, or a
whole tree (secrets/<lab>/<mount>/<prefix> against a recursive Vault listing).
Values are compared by sha256 and never printed; -v adds the hash prefixes.

Result per path:
  =       equal
  ~       different (per key: changed, only in git, only in Vault)
  +git    only in git (not in Vault yet)
  +vault  only in Vault (not in git yet: claims secret sync vault-to-sops)

Derived paths (secrets/<lab>/.derived, --exclude) are skipped.

Exit codes: 0 no drift, 5 drift, 1 error (a file that does not decrypt, Vault
unreachable, ...).`,
	Example: `  claims secret diff secrets/labul/kv/machinery-dev/argocd-oidc.enc.yaml
  claims secret diff kv/ --lab labul
  claims secret diff secrets/labul/kv/ -o json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSecretDiff(cmd.Context(), args[0], os.Stdout))
	},
}

func init() {
	f := secretDiffCmd.Flags()
	f.StringSliceVar(&secretDiffFlags.exclude, "exclude", nil, "Skip Vault paths matching these globs (mount/path; * ** ?), in addition to secrets/<lab>/.derived")
	f.StringVarP(&secretDiffFlags.output, "output", "o", "", "Output format: '' (text) or json")
	f.BoolVarP(&secretDiffFlags.verbose, "verbose", "v", false, "Show the sha256 prefixes of differing keys")
	secretCmd.AddCommand(secretDiffCmd)
}

// Path states.
const (
	stateEqual     = "equal"
	stateDifferent = "different"
	stateOnlyGit   = "only-git"
	stateOnlyVault = "only-vault"
	stateError     = "error"
)

type keyDiff struct {
	Key       string `json:"key"`
	State     string `json:"state"` // changed, only-git, only-vault
	GitHash   string `json:"gitSha256,omitempty"`
	VaultHash string `json:"vaultSha256,omitempty"`
}

type pathDiff struct {
	VaultPath string    `json:"vaultPath"`
	File      string    `json:"file"`
	State     string    `json:"state"`
	Keys      []keyDiff `json:"keys,omitempty"`
	Note      string    `json:"note,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type diffReport struct {
	Lab      string     `json:"lab"`
	Scope    string     `json:"scope"`
	Paths    []pathDiff `json:"paths"`
	Excluded []string   `json:"excluded,omitempty"`
}

func (r *diffReport) counts() map[string]int {
	c := map[string]int{}
	for _, p := range r.Paths {
		c[p.State]++
	}
	return c
}

// compareValues lists the keys that differ (empty: equal).
func compareValues(git, vlt map[string]string) []keyDiff {
	var out []keyDiff
	for k, gv := range git {
		vv, ok := vlt[k]
		switch {
		case !ok:
			out = append(out, keyDiff{Key: k, State: "only-git", GitHash: sops.ShortHash(gv)})
		case vv != gv:
			out = append(out, keyDiff{Key: k, State: "changed", GitHash: sops.ShortHash(gv), VaultHash: sops.ShortHash(vv)})
		}
	}
	for k, vv := range vlt {
		if _, ok := git[k]; !ok {
			out = append(out, keyDiff{Key: k, State: "only-vault", VaultHash: sops.ShortHash(vv)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// secretExcluder combines --exclude with secrets/<lab>/.derived.
func secretExcluder(root, lab string, extra []string) (*secretpath.Excluder, error) {
	e, err := secretpath.NewExcluder(extra...)
	if err != nil {
		return nil, usagef("%v", err)
	}
	if err := e.LoadDenylist(filepath.Join(root, secretpath.DenylistFile(lab))); err != nil {
		return nil, err
	}
	return e, nil
}

func runSecretDiff(ctx context.Context, arg string, out io.Writer) error {
	ctx = ctxOrBackground(ctx)
	root, err := secretRepoRoot()
	if err != nil {
		return err
	}
	if err := checkSOPSBinary(ctx); err != nil {
		return err
	}
	var rep *diffReport
	if isTreeArg(root, arg) {
		tree, err := resolveSecretTree(root, arg)
		if err != nil {
			return err
		}
		ex, err := secretExcluder(root, tree.Lab, secretDiffFlags.exclude)
		if err != nil {
			return err
		}
		vc, err := newVaultClient(ctx, tree.Lab)
		if err != nil {
			return err
		}
		if rep, err = diffTree(ctx, root, tree, vc, ex); err != nil {
			return err
		}
	} else {
		t, err := resolveSecretTarget(root, arg)
		if err != nil {
			return err
		}
		vc, err := newVaultClient(ctx, t.Ref.Lab)
		if err != nil {
			return err
		}
		rep = &diffReport{Lab: t.Ref.Lab, Scope: t.Ref.VaultPath(), Paths: []pathDiff{diffOne(ctx, t, vc, true, true)}}
	}
	if err := printDiff(rep, out); err != nil {
		return err
	}
	c := rep.counts()
	if c[stateError] > 0 {
		return fmt.Errorf("%s could not be compared", plural(c[stateError], "path"))
	}
	if n := c[stateDifferent] + c[stateOnlyGit] + c[stateOnlyVault]; n > 0 {
		return &driftError{n}
	}
	return nil
}

// diffOne compares one path; inGit/inVault say what the listings saw.
func diffOne(ctx context.Context, t secretTarget, vc *vault.Client, inGit, inVault bool) pathDiff {
	d := pathDiff{VaultPath: t.Ref.VaultPath(), File: t.Rel}
	var gitVals, vaultVals map[string]string
	if inGit && t.exists() {
		m, _, err := readSecretFile(ctx, t)
		if err != nil {
			d.State, d.Error = stateError, err.Error()
			return d
		}
		gitVals = m.Values
	}
	if inVault {
		v, err := vc.Get(ctx, t.Ref.Mount, t.Ref.Path)
		switch {
		case errors.Is(err, vault.ErrNotFound):
		case errors.Is(err, vault.ErrNotStrings) && gitVals == nil:
			// Cannot become a SOPS file as it is, but it is drift all the same.
			d.State, d.Note = stateOnlyVault, "values are not all strings: cannot be synced as they are"
			return d
		case err != nil:
			d.State, d.Error = stateError, err.Error()
			return d
		default:
			vaultVals = v
		}
	}
	switch {
	case gitVals == nil && vaultVals == nil:
		d.State, d.Error = stateError, "neither in git nor in Vault"
	case vaultVals == nil:
		d.State = stateOnlyGit
	case gitVals == nil:
		d.State = stateOnlyVault
	default:
		d.Keys = compareValues(gitVals, vaultVals)
		d.State = stateEqual
		if len(d.Keys) > 0 {
			d.State = stateDifferent
		}
	}
	return d
}

// diffTree compares every path below tree on both sides.
func diffTree(ctx context.Context, root string, tree secretpath.TreeRef, vc *vault.Client, ex *secretpath.Excluder) (*diffReport, error) {
	rep := &diffReport{Lab: tree.Lab, Scope: tree.VaultPath()}
	gitRefs, err := gitSecrets(root, tree)
	if err != nil {
		return nil, err
	}
	vaultPaths, err := vc.List(ctx, tree.Mount, tree.Prefix)
	if err != nil {
		return nil, err
	}
	type sides struct{ git, vault bool }
	all := map[string]*sides{}
	for _, r := range gitRefs {
		all[r.Path] = &sides{git: true}
	}
	for _, p := range vaultPaths {
		if s, ok := all[p]; ok {
			s.vault = true
		} else {
			all[p] = &sides{vault: true}
		}
	}
	paths := make([]string, 0, len(all))
	for p := range all {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		ref, err := secretpath.ParseVault(tree.Lab, tree.Mount+"/"+p)
		if err != nil {
			rep.Paths = append(rep.Paths, pathDiff{VaultPath: tree.Mount + "/" + p, State: stateError, Error: err.Error()})
			continue
		}
		if pat, ok := ex.Match(ref.VaultPath()); ok {
			rep.Excluded = append(rep.Excluded, ref.VaultPath())
			if all[p].git {
				warnf(fmt.Sprintf("%s is excluded as derived (%s) but has a SOPS file: derived secrets do not belong in git", ref.VaultPath(), pat))
			}
			continue
		}
		rep.Paths = append(rep.Paths, diffOne(ctx, targetFor(root, ref), vc, all[p].git, all[p].vault))
	}
	return rep, nil
}

func printDiff(rep *diffReport, out io.Writer) error {
	switch secretDiffFlags.output {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	case "":
	default:
		return usagef("--output %q: use json", secretDiffFlags.output)
	}
	marks := map[string]string{stateEqual: "=", stateDifferent: "~", stateOnlyGit: "+git", stateOnlyVault: "+vault", stateError: "ERR"}
	for _, p := range rep.Paths {
		line := fmt.Sprintf("%-6s %s", marks[p.State], p.VaultPath)
		switch p.State {
		case stateOnlyGit:
			line += "   (only in git: " + p.File + ")"
		case stateOnlyVault:
			line += "   (only in Vault)"
			if p.Note != "" {
				line += " " + p.Note
			}
		case stateError:
			line += "   " + p.Error
		}
		_, _ = fmt.Fprintln(out, line)
		for _, k := range p.Keys {
			kl := fmt.Sprintf("         %-10s %s", k.State, k.Key)
			if secretDiffFlags.verbose {
				kl += fmt.Sprintf("   git %s  vault %s", orDash(k.GitHash), orDash(k.VaultHash))
			}
			_, _ = fmt.Fprintln(out, kl)
		}
	}
	c := rep.counts()
	summary := fmt.Sprintf("\n%s/%s: %d equal, %d different, %d only in git, %d only in Vault",
		rep.Lab, rep.Scope, c[stateEqual], c[stateDifferent], c[stateOnlyGit], c[stateOnlyVault])
	if c[stateError] > 0 {
		summary += fmt.Sprintf(", %d errors", c[stateError])
	}
	if len(rep.Excluded) > 0 {
		summary += fmt.Sprintf(", %d excluded as derived", len(rep.Excluded))
	}
	_, _ = fmt.Fprintln(out, strings.TrimLeft(summary, "\n"))
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
