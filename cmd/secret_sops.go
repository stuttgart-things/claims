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
	"text/tabwriter"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/sops"
)

var secretSopsCmd = &cobra.Command{
	Use:   "sops",
	Short: "View and edit SOPS secret files (view, set, unset, rotate)",
	Long: `Edit one key of a SOPS file without plaintext on disk: sops decrypts into
memory, claims changes the key, sops encrypts from stdin for the recipients of
the matching creation rule in .sops.yaml (--filename-override), and the file is
decrypted again to check what was written (read-back).

Without --create-pr / --git-branch the change stays in the working tree.`,
}

// --- view --------------------------------------------------------------------

var secretViewFlags struct {
	reveal bool
	keys   []string
	output string
}

var secretViewCmd = &cobra.Command{
	Use:   "view <file|vault-path>",
	Short: "Show the keys of a SOPS secret: length and sha256 prefix (values with --reveal)",
	Example: `  claims secret sops view secrets/labul/kv/app/db.enc.yaml
  claims secret sops view kv/app/db --lab labul --reveal --key password   # the value only`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSecretView(cmd.Context(), args[0], os.Stdout))
	},
}

type secretKeyInfo struct {
	Key    string `json:"key"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
	Value  string `json:"value,omitempty"`
}

func runSecretView(ctx context.Context, arg string, out io.Writer) error {
	ctx = ctxOrBackground(ctx)
	root, err := secretRepoRoot()
	if err != nil {
		return err
	}
	t, err := resolveSecretTarget(root, arg)
	if err != nil {
		return err
	}
	if !t.exists() {
		return fmt.Errorf("%s does not exist (Vault path %s)", t.Rel, t.Ref)
	}
	m, ciphertext, err := readSecretFile(ctx, t)
	if err != nil {
		return err
	}
	keys := sops.Keys(m.Values)
	if len(secretViewFlags.keys) > 0 {
		for _, k := range secretViewFlags.keys {
			if _, ok := m.Values[k]; !ok {
				return fmt.Errorf("%s has no key %s (keys: %s)", t.Rel, k, strings.Join(keys, ", "))
			}
		}
		keys = secretViewFlags.keys
	}
	// --reveal --key k: the bare value, for scripts.
	if secretViewFlags.reveal && len(secretViewFlags.keys) == 1 && secretViewFlags.output == "" {
		_, _ = fmt.Fprint(out, m.Values[keys[0]])
		if isTTY(os.Stdout) {
			_, _ = fmt.Fprintln(out)
		}
		return nil
	}
	infos := make([]secretKeyInfo, 0, len(keys))
	for _, k := range keys {
		v := m.Values[k]
		i := secretKeyInfo{Key: k, Length: len(v), SHA256: sops.ShortHash(v)}
		if secretViewFlags.reveal {
			i.Value = v
		}
		infos = append(infos, i)
	}
	switch secretViewFlags.output {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"file": t.Rel, "lab": t.Ref.Lab, "vaultPath": t.Ref.VaultPath(),
			"name": m.Name(), "namespace": m.Namespace(), "recipients": sops.FileRecipients(ciphertext), "keys": infos,
		})
	case "":
	default:
		return usagef("--output %q: use json", secretViewFlags.output)
	}
	_, _ = fmt.Fprintf(out, "File:        %s\n", t.Rel)
	_, _ = fmt.Fprintf(out, "Vault:       %s (lab %s)\n", t.Ref.VaultPath(), t.Ref.Lab)
	_, _ = fmt.Fprintf(out, "Secret:      %s/%s\n", m.Namespace(), m.Name())
	var short []string
	for _, r := range sops.FileRecipients(ciphertext) {
		short = append(short, sops.ShortRecipient(r))
	}
	_, _ = fmt.Fprintf(out, "Recipients:  %s\n\n", strings.Join(short, ", "))
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if secretViewFlags.reveal {
		_, _ = fmt.Fprintln(tw, "KEY\tLENGTH\tSHA256\tVALUE")
	} else {
		_, _ = fmt.Fprintln(tw, "KEY\tLENGTH\tSHA256")
	}
	for _, i := range infos {
		if secretViewFlags.reveal {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", i.Key, i.Length, i.SHA256, i.Value)
		} else {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\n", i.Key, i.Length, i.SHA256)
		}
	}
	return tw.Flush()
}

// --- set ---------------------------------------------------------------------

var secretSetFlags struct {
	generate  bool
	length    int
	charset   string
	overwrite bool
	git       secretGit
}

var secretSetCmd = &cobra.Command{
	Use:   "set <file|vault-path> <key=value|key=@file|key>...",
	Short: "Set keys of a SOPS secret (value, @file, stdin, or --generate); creates the file if needed",
	Long: `Sets one or more keys. Each key is given as:

  key=value   the value from the command line (ends up in the shell history)
  key=@file   the content of file, byte for byte
  key         the value from stdin (one trailing newline removed), or a hidden
              prompt on a terminal; with --generate a random value instead

--generate creates the values in the SOPS file itself, so a generated declared
secret exists in git before it reaches Vault. A key that already has a value is
kept (generate-once) unless --overwrite is given.

A file that does not exist yet is created as an Opaque Secret named after its
path (see ` + "`claims secret path -o yaml`" + `) in --namespace.`,
	Example: `  claims secret sops set kv/app/db --lab labul username=app password=@./pw.txt
  printf %s "$TOKEN" | claims secret sops set secrets/labul/kv/ci/github.enc.yaml token
  claims secret sops set kv/app/db --lab labul password --generate --length 40 --charset symbols`,
	Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSecretSet(cmd.Context(), args[0], args[1:], os.Stdin, os.Stdout))
	},
}

// promptSecret asks for a hidden value on a terminal; a variable for tests.
var promptSecret = func(key string) (string, error) {
	var v string
	err := huh.NewInput().Title("Value of " + key).EchoMode(huh.EchoModePassword).Value(&v).Run()
	return v, err
}

type assignment struct {
	key   string
	value string
	gen   bool
}

// parseAssignments reads the key arguments of set.
func parseAssignments(args []string, stdin io.Reader, stdinTTY bool) ([]assignment, error) {
	var out []assignment
	seen := map[string]bool{}
	fromStdin := 0
	for _, a := range args {
		key, val, hasVal := strings.Cut(a, "=")
		if err := sops.ValidKey(key); err != nil {
			return nil, usagef("%v", err)
		}
		if seen[key] {
			return nil, usagef("key %s given twice", key)
		}
		seen[key] = true
		switch {
		case hasVal && secretSetFlags.generate:
			return nil, usagef("%s: --generate makes the value; give the key alone", key)
		case hasVal && strings.HasPrefix(val, "@"):
			b, err := os.ReadFile(val[1:])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out = append(out, assignment{key: key, value: string(b)})
		case hasVal:
			out = append(out, assignment{key: key, value: val})
		case secretSetFlags.generate:
			out = append(out, assignment{key: key, gen: true})
		default:
			fromStdin++
			if fromStdin > 1 && !stdinTTY {
				return nil, usagef("only one key can read its value from stdin")
			}
			var v string
			if stdinTTY {
				var err error
				if v, err = promptSecret(key); err != nil {
					return nil, err
				}
			} else {
				b, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
				if err != nil {
					return nil, err
				}
				v = strings.TrimSuffix(string(b), "\n")
			}
			if v == "" {
				return nil, fmt.Errorf("%s: empty value", key)
			}
			out = append(out, assignment{key: key, value: v})
		}
	}
	return out, nil
}

func runSecretSet(ctx context.Context, arg string, keyArgs []string, stdin io.Reader, out io.Writer) error {
	ctx = ctxOrBackground(ctx)
	root, err := secretRepoRoot()
	if err != nil {
		return err
	}
	t, err := resolveSecretTarget(root, arg)
	if err != nil {
		return err
	}
	charset := ""
	if secretSetFlags.generate {
		if charset, err = resolveCharset(secretSetFlags.charset); err != nil {
			return err
		}
		if _, err := generateValue(secretSetFlags.length, charset); err != nil {
			return err // a bad --length before anything is read
		}
	}
	if err := checkSOPSBinary(ctx); err != nil {
		return err
	}
	if _, err := sopsRule(t); err != nil {
		return err
	}
	if err := secretSetFlags.git.preflight(ctx, root); err != nil {
		return err
	}
	in, ok := stdin.(*os.File)
	assigns, err := parseAssignments(keyArgs, stdin, ok && isTTY(in))
	if err != nil {
		return err
	}

	var m *sops.Manifest
	created := !t.exists()
	if created {
		m = newSecretManifest(t.Ref, secretOpts.namespace)
	} else if m, _, err = readSecretFile(ctx, t); err != nil {
		return err
	}
	var changed, kept []string
	for _, a := range assigns {
		_, exists := m.Values[a.key]
		if a.gen {
			if exists && !secretSetFlags.overwrite {
				kept = append(kept, a.key)
				continue
			}
			if a.value, err = generateValue(secretSetFlags.length, charset); err != nil {
				return err
			}
		}
		if exists && m.Values[a.key] == a.value {
			kept = append(kept, a.key)
			continue
		}
		m.Values[a.key] = a.value
		changed = append(changed, a.key)
	}
	if len(kept) > 0 {
		_, _ = fmt.Fprintf(out, "Unchanged: %s (already set; --generate keeps existing values unless --overwrite)\n", strings.Join(kept, ", "))
	}
	if len(changed) == 0 {
		_, _ = fmt.Fprintf(out, "%s: nothing to change\n", t.Rel)
		return nil
	}
	if err := writeSecretFile(ctx, t, m); err != nil {
		return err
	}
	verb := "Updated"
	if created {
		verb = "Created"
	}
	_, _ = fmt.Fprintln(out, successStyle.Render(fmt.Sprintf("%s %s: set %s (read-back ok)", verb, t.Rel, strings.Join(changed, ", "))))
	return secretSetFlags.git.commit(root, secretChange{
		Command: "set", Lab: t.Ref.Lab, Files: []string{t.Abs},
		Subject: fmt.Sprintf("secret: set %s in %s", strings.Join(changed, ", "), t.Ref.VaultPath()),
		Body:    fmt.Sprintf("SOPS file `%s` (Vault `%s`, lab %s): keys set: %s.", t.Rel, t.Ref.VaultPath(), t.Ref.Lab, strings.Join(changed, ", ")),
	}, out)
}

// --- unset -------------------------------------------------------------------

var secretUnsetFlags struct {
	ignoreMissing bool
	git           secretGit
}

var secretUnsetCmd = &cobra.Command{
	Use:     "unset <file|vault-path> <key>...",
	Short:   "Remove keys from a SOPS secret",
	Example: `  claims secret sops unset kv/app/db --lab labul old-password`,
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSecretUnset(cmd.Context(), args[0], args[1:], os.Stdout))
	},
}

func runSecretUnset(ctx context.Context, arg string, keys []string, out io.Writer) error {
	ctx = ctxOrBackground(ctx)
	root, err := secretRepoRoot()
	if err != nil {
		return err
	}
	t, err := resolveSecretTarget(root, arg)
	if err != nil {
		return err
	}
	if !t.exists() {
		return fmt.Errorf("%s does not exist", t.Rel)
	}
	if err := checkSOPSBinary(ctx); err != nil {
		return err
	}
	if err := secretUnsetFlags.git.preflight(ctx, root); err != nil {
		return err
	}
	m, _, err := readSecretFile(ctx, t)
	if err != nil {
		return err
	}
	var removed, missing []string
	for _, k := range keys {
		if _, ok := m.Values[k]; !ok {
			missing = append(missing, k)
			continue
		}
		delete(m.Values, k)
		removed = append(removed, k)
	}
	if len(missing) > 0 && !secretUnsetFlags.ignoreMissing {
		return fmt.Errorf("%s has no key %s (--ignore-missing skips missing keys)", t.Rel, strings.Join(missing, ", "))
	}
	if len(removed) == 0 {
		_, _ = fmt.Fprintf(out, "%s: nothing to remove\n", t.Rel)
		return nil
	}
	if len(m.Values) == 0 {
		return fmt.Errorf("removing %s would leave %s without keys: delete the file instead (git rm %s)", strings.Join(removed, ", "), t.Rel, t.Rel)
	}
	if err := writeSecretFile(ctx, t, m); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, successStyle.Render(fmt.Sprintf("Updated %s: removed %s (read-back ok)", t.Rel, strings.Join(removed, ", "))))
	return secretUnsetFlags.git.commit(root, secretChange{
		Command: "unset", Lab: t.Ref.Lab, Files: []string{t.Abs},
		Subject: fmt.Sprintf("secret: remove %s from %s", strings.Join(removed, ", "), t.Ref.VaultPath()),
		Body:    fmt.Sprintf("SOPS file `%s` (Vault `%s`, lab %s): keys removed: %s.", t.Rel, t.Ref.VaultPath(), t.Ref.Lab, strings.Join(removed, ", ")),
	}, out)
}

// --- rotate ------------------------------------------------------------------

var secretRotateFlags struct {
	git secretGit
}

var secretRotateCmd = &cobra.Command{
	Use:   "rotate <file|vault-path|dir>...",
	Short: "Re-encrypt SOPS secrets for the recipients of .sops.yaml (updatekeys + new data key)",
	Long: `For every file: sops updatekeys (the recipients of the matching creation rule
now: a new lab key, an added recovery key, a removed one), then sops rotate (a
new data key, every value re-encrypted). Both work in place on the encrypted
file. The values are checked to be unchanged afterwards.

The values themselves are not changed: to rotate a secret's value use
` + "`claims secret sops set --generate --overwrite`" + `.`,
	Example: `  claims secret sops rotate secrets/labul/
  claims secret sops rotate kv/app/db --lab labul --create-pr`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSecretRotate(cmd.Context(), args, os.Stdout))
	},
}

func runSecretRotate(ctx context.Context, args []string, out io.Writer) error {
	ctx = ctxOrBackground(ctx)
	root, err := secretRepoRoot()
	if err != nil {
		return err
	}
	var targets []secretTarget
	for _, a := range args {
		if isTreeArg(root, a) {
			refs, err := gitSecretsBelow(root, a)
			if err != nil {
				return err
			}
			for _, r := range refs {
				targets = append(targets, targetFor(root, r))
			}
			continue
		}
		t, err := resolveSecretTarget(root, a)
		if err != nil {
			return err
		}
		if !t.exists() {
			return fmt.Errorf("%s does not exist", t.Rel)
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		return errors.New("no SOPS files found")
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Rel < targets[j].Rel })
	if err := checkSOPSBinary(ctx); err != nil {
		return err
	}
	for _, t := range targets {
		if _, err := sopsRule(t); err != nil {
			return err
		}
	}
	if err := secretRotateFlags.git.preflight(ctx, root); err != nil {
		return err
	}
	var files, lines []string
	for _, t := range targets {
		before, encBefore, err := readSecretFile(ctx, t)
		if err != nil {
			return err
		}
		if err := sops.UpdateKeys(ctx, root, t.Rel); err != nil {
			return err
		}
		if err := sops.Rotate(ctx, root, t.Rel); err != nil {
			return err
		}
		after, encAfter, err := readSecretFile(ctx, t)
		if err != nil {
			return fmt.Errorf("read-back of %s: %w", t.Rel, err)
		}
		if !sops.SameValues(before.Values, after.Values) {
			return fmt.Errorf("read-back of %s: values changed during rotation; restore it with git checkout -- %s", t.Rel, t.Rel)
		}
		if err := sops.CheckEncrypted(encAfter); err != nil {
			return fmt.Errorf("%s: %w; restore it with git checkout -- %s", t.Rel, err, t.Rel)
		}
		rb, ra := recipientSet(encBefore), recipientSet(encAfter)
		line := fmt.Sprintf("%s: new data key, %d recipient(s)", t.Rel, len(ra))
		if d := recipientDelta(rb, ra); d != "" {
			line += " (" + d + ")"
		}
		_, _ = fmt.Fprintln(out, successStyle.Render("Rotated "+line))
		lines = append(lines, "- `"+line+"`")
		files = append(files, filepath.Join(root, filepath.FromSlash(t.Rel)))
	}
	return secretRotateFlags.git.commit(root, secretChange{
		Command: "rotate", Lab: targets[0].Ref.Lab, Files: files,
		Subject: fmt.Sprintf("secret: re-encrypt %s for the recipients of .sops.yaml", plural(len(files), "SOPS file")),
		Body:    "sops updatekeys + sops rotate, values unchanged (read-back checked):\n\n" + strings.Join(lines, "\n"),
	}, out)
}

func recipientSet(enc []byte) map[string]bool {
	s := map[string]bool{}
	for _, r := range sops.FileRecipients(enc) {
		s[r] = true
	}
	return s
}

// recipientDelta says which recipients were added and removed.
func recipientDelta(before, after map[string]bool) string {
	var add, del []string
	for r := range after {
		if !before[r] {
			add = append(add, "+"+sops.ShortRecipient(r))
		}
	}
	for r := range before {
		if !after[r] {
			del = append(del, "-"+sops.ShortRecipient(r))
		}
	}
	sort.Strings(add)
	sort.Strings(del)
	return strings.Join(append(add, del...), " ")
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func init() {
	vf := secretViewCmd.Flags()
	vf.BoolVar(&secretViewFlags.reveal, "reveal", false, "Print the values (with exactly one --key: the bare value)")
	vf.StringSliceVarP(&secretViewFlags.keys, "key", "k", nil, "Only these keys")
	vf.StringVarP(&secretViewFlags.output, "output", "o", "", "Output format: '' (table) or json")

	sf := secretSetCmd.Flags()
	sf.BoolVar(&secretSetFlags.generate, "generate", false, "Generate random values for the keys given without a value")
	sf.IntVar(&secretSetFlags.length, "length", 32, "Length of generated values")
	sf.StringVar(&secretSetFlags.charset, "charset", "alnum", "Characters of generated values: "+charsetNames()+", or custom:<characters>")
	sf.BoolVar(&secretSetFlags.overwrite, "overwrite", false, "With --generate: replace existing values too")
	addNamespaceFlag(secretSetCmd)
	addSecretGitFlags(secretSetCmd, &secretSetFlags.git)

	secretUnsetCmd.Flags().BoolVar(&secretUnsetFlags.ignoreMissing, "ignore-missing", false, "Skip keys the file does not have")
	addSecretGitFlags(secretUnsetCmd, &secretUnsetFlags.git)
	addSecretGitFlags(secretRotateCmd, &secretRotateFlags.git)

	secretSopsCmd.AddCommand(secretViewCmd, secretSetCmd, secretUnsetCmd, secretRotateCmd)
	secretCmd.AddCommand(secretSopsCmd)

}
