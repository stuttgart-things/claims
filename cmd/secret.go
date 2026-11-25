package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/endpoint"
	"github.com/stuttgart-things/claims/internal/secretpath"
	"github.com/stuttgart-things/claims/internal/sops"
	"github.com/stuttgart-things/claims/internal/vault"
)

// claims secret (claims#122, crossplane-configurations#572): SOPS files in
// git are the master of declared secrets, Vault is the runtime. The file
// path is the Vault path:
//
//	secrets/<lab>/<mount>/<path>.enc.yaml  <=>  <mount>/<path> (KV v2) in the lab's Vault
//
// Every command takes a file or a Vault path (with --lab). Plaintext lives
// only in memory; values are never printed unless --reveal asks for it.

const groupSecrets = "secrets"

// EnvLab is the default lab of claims secret (like --lab).
const EnvLab = "CLAIMS_LAB"

// secretOpts are the flags shared by every claims secret command.
var secretOpts struct {
	repo         string
	lab          string
	vaultAddr    string
	vaultToken   string
	vaultAuth    string
	vaultCAFile  string
	appRoleMount string
	namespace    string
}

var secretCmd = &cobra.Command{
	Use:     "secret",
	GroupID: groupSecrets,
	Short:   "Manage SOPS secrets in git and their Vault entries (path, sops, diff, sync, vault)",
	Long: `SOPS files in the fleet repository are the master of declared secrets; Vault
is the runtime. The file path is the Vault path, 1:1:

  secrets/<lab>/<mount>/<path>.enc.yaml   <=>   <mount>/<path> (KV v2) in the Vault of <lab>

Every file is a Kubernetes Secret (stringData, one key per Vault key) encrypted
for the recipients of the creation rule in the repository's .sops.yaml.

Commands accept a file (secrets/labul/kv/app/db.enc.yaml) or a Vault path with
--lab (kv/app/db --lab labul). The lab defaults to $CLAIMS_LAB, then the current
lab of ` + "`claims secret vault use`" + `.

Values never touch the disk in plaintext and are never printed unless --reveal
asks for it: view and diff show keys, lengths and sha256 prefixes.`,
	Example: `  claims secret path kv/machinery-dev/argocd-oidc --lab labul
  claims secret sops view secrets/labul/kv/machinery-dev/argocd-oidc.enc.yaml
  claims secret sops set kv/app/db --lab labul password --generate --length 40
  claims secret diff secrets/labul/kv/
  claims secret sync vault-to-sops kv/machinery-dev/ --lab labul --create-pr`,
}

func init() {
	rootCmd.AddGroup(&cobra.Group{ID: groupSecrets, Title: "Secrets:"})
	f := secretCmd.PersistentFlags()
	f.StringVarP(&secretOpts.repo, "repo", "C", ".", "Checkout of the fleet repository (its root holds .sops.yaml and secrets/)")
	f.StringVar(&secretOpts.lab, "lab", "", "Lab of a Vault path (default: $"+EnvLab+", then claims secret vault use)")
	f.StringVar(&secretOpts.vaultAddr, "vault-addr", "", "Vault address (default: the lab's Vault from the config, then $VAULT_ADDR)")
	f.StringVar(&secretOpts.vaultToken, "vault-token", "", "Vault token (default: $VAULT_TOKEN, AppRole via $VAULT_ROLE_ID/$VAULT_SECRET_ID, ~/.vault-token)")
	f.StringVar(&secretOpts.vaultAuth, "vault-auth", "", "Vault auth method: token or approle (default: the lab's config, else what is found)")
	f.StringVar(&secretOpts.vaultCAFile, "vault-ca-file", "", "PEM file with the CA of the Vault (default: the lab's config, then $VAULT_CACERT)")
	f.StringVar(&secretOpts.appRoleMount, "vault-approle-mount", "", "Mount of the AppRole auth method (default: the lab's config, else approle)")
	rootCmd.AddCommand(secretCmd)
}

// secretRepoRoot is the root of the fleet checkout (--repo).
func secretRepoRoot() (string, error) {
	root, err := findRepoRoot(secretOpts.repo)
	if err != nil {
		return "", usagef("%s is not in a git checkout: run in a checkout of the fleet repository or pass --repo <dir>", secretOpts.repo)
	}
	return root, nil
}

// secretLab is --lab, $CLAIMS_LAB, else the current lab of the config.
func secretLab() (string, error) {
	if secretOpts.lab != "" {
		return secretOpts.lab, nil
	}
	if l := os.Getenv(EnvLab); l != "" {
		return l, nil
	}
	if cfg, err := loadClaimsConfig(); err == nil && cfg.CurrentVault != "" {
		return cfg.CurrentVault, nil
	}
	return "", usagef("a Vault path needs a lab: pass --lab <lab>, set $%s, or `claims secret vault use <lab>`", EnvLab)
}

func loadClaimsConfig() (*endpoint.Config, error) {
	p, err := endpoint.DefaultPath()
	if err != nil {
		return nil, err
	}
	return endpoint.Load(p)
}

// secretTarget is one secret: its Ref and its file in the checkout.
type secretTarget struct {
	Ref      secretpath.Ref
	RepoRoot string
	Rel      string // slash-separated, relative to RepoRoot
	Abs      string
}

func (t secretTarget) exists() bool {
	_, err := os.Stat(t.Abs)
	return err == nil
}

// isFileArg reports whether arg names a file or directory of secrets/
// rather than a Vault path.
func isFileArg(arg string) bool {
	a := filepath.ToSlash(arg)
	if strings.HasSuffix(a, secretpath.Suffix) {
		return true
	}
	if a == secretpath.Root || strings.HasPrefix(a, secretpath.Root+"/") || strings.HasPrefix(a, "./"+secretpath.Root+"/") {
		return true
	}
	return filepath.IsAbs(arg)
}

// relToRepo makes arg (a path relative to the working directory or to the
// repository root, or absolute) relative to root.
func relToRepo(root, arg string) (string, error) {
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		if filepath.IsAbs(arg) {
			return "", fmt.Errorf("%s is outside the checkout %s", arg, root)
		}
		rel = filepath.Clean(arg) // relative to the repository root
	}
	return filepath.ToSlash(rel), nil
}

// labCheck rejects a --lab that contradicts the lab of a file.
func labCheck(fileLab string) error {
	if secretOpts.lab != "" && secretOpts.lab != fileLab {
		return usagef("--lab %s, but the file belongs to lab %s", secretOpts.lab, fileLab)
	}
	return nil
}

// resolveSecretTarget turns a file or a Vault path into a secretTarget.
func resolveSecretTarget(root, arg string) (secretTarget, error) {
	var ref secretpath.Ref
	if isFileArg(arg) {
		rel, err := relToRepo(root, arg)
		if err != nil {
			return secretTarget{}, err
		}
		if ref, err = secretpath.ParseFile(rel); err != nil {
			return secretTarget{}, usagef("%v", err)
		}
		if err := labCheck(ref.Lab); err != nil {
			return secretTarget{}, err
		}
	} else {
		lab, err := secretLab()
		if err != nil {
			return secretTarget{}, err
		}
		if ref, err = secretpath.ParseVault(lab, arg); err != nil {
			return secretTarget{}, usagef("%v", err)
		}
	}
	return targetFor(root, ref), nil
}

func targetFor(root string, ref secretpath.Ref) secretTarget {
	rel := ref.File()
	return secretTarget{Ref: ref, RepoRoot: root, Rel: rel, Abs: filepath.Join(root, filepath.FromSlash(rel))}
}

// isTreeArg reports whether arg names a directory/prefix rather than one secret.
func isTreeArg(root, arg string) bool {
	a := filepath.ToSlash(arg)
	if strings.HasSuffix(a, secretpath.Suffix) {
		return false
	}
	if strings.HasSuffix(a, "/") {
		return true
	}
	if isFileArg(arg) {
		return true // secrets/<lab>/<mount>[/<prefix>] without the suffix
	}
	if root != "" {
		if lab, err := secretLab(); err == nil {
			if st, err := os.Stat(filepath.Join(root, secretpath.Root, lab, filepath.FromSlash(a))); err == nil && st.IsDir() {
				if _, err := os.Stat(filepath.Join(root, secretpath.Root, lab, filepath.FromSlash(a)+secretpath.Suffix)); err != nil {
					return true
				}
			}
		}
	}
	return !strings.Contains(strings.Trim(a, "/"), "/") // a mount alone
}

// resolveSecretTree turns a directory or a Vault prefix into a TreeRef.
func resolveSecretTree(root, arg string) (secretpath.TreeRef, error) {
	if isFileArg(arg) {
		rel, err := relToRepo(root, arg)
		if err != nil {
			return secretpath.TreeRef{}, err
		}
		t, err := secretpath.ParseDir(rel)
		if err != nil {
			return secretpath.TreeRef{}, usagef("%v", err)
		}
		return t, labCheck(t.Lab)
	}
	lab, err := secretLab()
	if err != nil {
		return secretpath.TreeRef{}, err
	}
	t, err := secretpath.ParseVaultTree(lab, arg)
	if err != nil {
		return secretpath.TreeRef{}, usagef("%v", err)
	}
	return t, nil
}

// gitSecretsBelow lists the secrets below a directory of the checkout
// (secrets/, secrets/<lab>, ... or a Vault prefix with the lab).
func gitSecretsBelow(root, arg string) ([]secretpath.Ref, error) {
	if !isFileArg(arg) {
		tree, err := resolveSecretTree(root, arg)
		if err != nil {
			return nil, err
		}
		return gitSecrets(root, tree)
	}
	rel, err := relToRepo(root, arg)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, filepath.FromSlash(rel))
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory of the checkout", rel)
	}
	var out []secretpath.Ref
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, secretpath.Suffix) {
			return err
		}
		r, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		ref, err := secretpath.ParseFile(filepath.ToSlash(r))
		if err != nil {
			return err
		}
		if secretOpts.lab == "" || ref.Lab == secretOpts.lab {
			out = append(out, ref)
		}
		return nil
	})
	return out, err
}

// gitSecrets lists the secrets of tree in the checkout.
func gitSecrets(root string, tree secretpath.TreeRef) ([]secretpath.Ref, error) {
	dir := filepath.Join(root, filepath.FromSlash(tree.Dir()))
	var out []secretpath.Ref
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && p == dir {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, secretpath.Suffix) {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		ref, err := secretpath.ParseFile(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		if tree.Contains(ref) {
			out = append(out, ref)
		}
		return nil
	})
	if errors.Is(err, filepath.SkipDir) {
		err = nil
	}
	return out, err
}

// --- Vault -------------------------------------------------------------------

// newVaultClient is how commands get a logged-in client for lab; a variable
// for tests.
var newVaultClient = func(ctx context.Context, lab string) (*vault.Client, error) {
	o, err := vaultOptions(lab)
	if err != nil {
		return nil, err
	}
	return vault.New(ctx, o)
}

// vaultOptions resolves the Vault of lab: flags, then the lab's entry in the
// config, then $VAULT_ADDR / $VAULT_CACERT.
func vaultOptions(lab string) (vault.Options, error) {
	o := vault.Options{
		Addr:         secretOpts.vaultAddr,
		CAFile:       secretOpts.vaultCAFile,
		AuthMethod:   secretOpts.vaultAuth,
		Token:        secretOpts.vaultToken,
		AppRoleMount: secretOpts.appRoleMount,
		Timeout:      30 * time.Second,
	}
	if cfg, err := loadClaimsConfig(); err == nil {
		if v, ok := cfg.GetVault(lab); ok {
			if o.Addr == "" {
				o.Addr = v.Addr
			}
			if o.CAFile == "" {
				o.CAFile = v.CAFile
			}
			if o.AuthMethod == "" {
				o.AuthMethod = v.Auth
			}
			if o.AppRoleMount == "" {
				o.AppRoleMount = v.AppRoleMount
			}
		}
	}
	if o.Addr == "" {
		if a := os.Getenv("VAULT_ADDR"); a != "" {
			o.Addr = a
			warnf(fmt.Sprintf("no Vault configured for lab %s: using $VAULT_ADDR (%s)", lab, a))
		}
	}
	if o.CAFile == "" {
		o.CAFile = os.Getenv("VAULT_CACERT")
	}
	if o.Addr == "" {
		return o, fmt.Errorf("no Vault for lab %s: `claims secret vault add %s <addr>`, --vault-addr, or $VAULT_ADDR", lab, lab)
	}
	return o, nil
}

// --- SOPS --------------------------------------------------------------------

// sopsRule returns the creation rule for t (claims never guesses recipients).
func sopsRule(t secretTarget) (*sops.Rule, error) {
	cfg, err := sops.LoadConfig(t.RepoRoot)
	if err != nil {
		return nil, err
	}
	return cfg.RuleFor(t.Rel)
}

// checkSOPSBinary fails unless sops is new enough for --filename-override.
func checkSOPSBinary(ctx context.Context) error {
	v, err := sops.Version(ctx)
	if err != nil {
		return err
	}
	if !sops.VersionAtLeast(v, sops.MinVersion) {
		return fmt.Errorf("sops %s is too old: claims needs %d.%d.%d or newer (--filename-override)", v, sops.MinVersion[0], sops.MinVersion[1], sops.MinVersion[2])
	}
	return nil
}

// readSecretFile decrypts t into a manifest.
func readSecretFile(ctx context.Context, t secretTarget) (*sops.Manifest, []byte, error) {
	enc, err := os.ReadFile(t.Abs)
	if err != nil {
		return nil, nil, err
	}
	if !sops.IsEncrypted(enc) {
		return nil, nil, fmt.Errorf("%s is not SOPS-encrypted: claims refuses plaintext below %s/", t.Rel, secretpath.Root)
	}
	plain, err := sops.Decrypt(ctx, t.RepoRoot, t.Rel)
	if err != nil {
		return nil, nil, err
	}
	m, err := sops.ParseManifest(plain)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", t.Rel, err)
	}
	return m, enc, nil
}

// writeSecretFile encrypts m for t, checks that every value came out
// encrypted, writes the file and reads it back.
func writeSecretFile(ctx context.Context, t secretTarget, m *sops.Manifest) error {
	if _, err := sopsRule(t); err != nil {
		return err
	}
	for k := range m.Values {
		if err := sops.ValidKey(k); err != nil {
			return err
		}
	}
	plain, err := m.Marshal()
	if err != nil {
		return err
	}
	enc, err := sops.EncryptFor(ctx, t.RepoRoot, t.Rel, plain, m.SecretValues())
	if err != nil {
		return err
	}
	if err := sops.CheckEncrypted(enc); err != nil {
		return fmt.Errorf("%s: %w; nothing written (check encrypted_regex in .sops.yaml)", t.Rel, err)
	}
	if err := sops.WriteFileAtomic(t.Abs, enc); err != nil {
		return err
	}
	back, _, err := readSecretFile(ctx, t)
	if err != nil {
		return fmt.Errorf("read-back of %s: %w", t.Rel, err)
	}
	if !sops.SameValues(back.Values, m.Values) {
		return fmt.Errorf("read-back of %s: the decrypted values differ from what was written", t.Rel)
	}
	return nil
}

// newSecretManifest is the Secret for a file that does not exist yet.
func newSecretManifest(ref secretpath.Ref, namespace string) *sops.Manifest {
	if namespace == "" {
		namespace = secretpath.DefaultNamespace
	}
	return sops.NewManifest(secretpath.SecretName(ref), namespace, map[string]string{
		sops.AnnotationLab:       ref.Lab,
		sops.AnnotationVaultPath: ref.VaultPath(),
	})
}

// addNamespaceFlag registers --namespace on c (for files claims creates).
func addNamespaceFlag(c *cobra.Command) {
	c.Flags().StringVarP(&secretOpts.namespace, "namespace", "n", secretpath.DefaultNamespace, "Namespace of Secrets claims creates (existing files keep theirs)")
}
