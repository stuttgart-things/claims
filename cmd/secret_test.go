package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/stuttgart-things/claims/internal/secretpath"
	"github.com/stuttgart-things/claims/internal/sops"
	"github.com/stuttgart-things/claims/internal/sops/sopstest"
	"github.com/stuttgart-things/claims/internal/vault/vaulttest"
)

// Throwaway values: none of them may show up in any output, error, commit
// or plaintext file.
const (
	tvUser     = "tv-user-7f3a91"
	tvPassword = "tv-password-c0ffee42"
	tvAPIKey   = "tv-apikey-d15ea5e"
	tvFromFile = "tv-fromfile-8badf00d\n"
	tvStdin    = "tv-stdin-feedface"
	tvChanged  = "tv-changed-deadbeef"
)

var testValues = []string{tvUser, tvPassword, tvAPIKey, strings.TrimSpace(tvFromFile), tvStdin, tvChanged}

type secretFixture struct {
	root   string
	remote string
	vault  *vaulttest.Server
	keys   *sopstest.Keys
}

// newSecretFixture: a fleet checkout with .sops.yaml for labul and labda
// (throwaway age keys, our key = labul), a bare remote, and a fake Vault.
func newSecretFixture(t *testing.T) *secretFixture {
	t.Helper()
	sopstest.RequireSOPS(t)
	if v, err := sops.Version(context.Background()); err != nil || !sops.VersionAtLeast(v, sops.MinVersion) {
		t.Skipf("sops %s too old or broken: %v", v, err)
	}
	f := &secretFixture{root: t.TempDir(), vault: vaulttest.New(t), keys: sopstest.NewKeys(t, "labul", "labda")}
	f.keys.WriteConfig(t, f.root, "labul", "labda")
	sopstest.UseKey(t, f.keys.Lab["labul"])
	if err := os.MkdirAll(filepath.Join(f.root, "secrets", "labul"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "secrets", "labul", ".derived"), []byte("# derived\nkv/kubeconfigs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainInitWithOptions(f.root, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	if err := wt.AddGlob(".sops.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("init", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	f.remote = t.TempDir()
	if _, err := git.PlainInit(f.remote, true); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{f.remote}}); err != nil {
		t.Fatal(err)
	}

	resetSecretFlags(t)
	secretOpts.repo = f.root
	secretOpts.vaultAddr = f.vault.URL
	secretOpts.vaultToken = f.vault.Token
	t.Setenv(EnvLab, "")
	return f
}

// resetSecretFlags restores every claims secret flag after the test.
func resetSecretFlags(t *testing.T) {
	t.Helper()
	so, sv, ss, su, sr, sy, s2, sd, ds, sp := secretOpts, secretViewFlags, secretSetFlags, secretUnsetFlags,
		secretRotateFlags, secretSyncFlags, secretSyncS2VFlags, secretDiffFlags, doctorSecretFlags, skipPreflight
	t.Cleanup(func() {
		secretOpts, secretViewFlags, secretSetFlags, secretUnsetFlags = so, sv, ss, su
		secretRotateFlags, secretSyncFlags, secretSyncS2VFlags, secretDiffFlags, doctorSecretFlags, skipPreflight = sr, sy, s2, sd, ds, sp
	})
	secretOpts.namespace = secretpath.DefaultNamespace
	secretSetFlags.length, secretSetFlags.charset = 32, "alnum"
	secretSetFlags.git = secretGit{remote: "origin", prBase: "main"}
	secretUnsetFlags.git = secretGit{remote: "origin", prBase: "main"}
	secretRotateFlags.git = secretGit{remote: "origin", prBase: "main"}
	secretSyncFlags.git = secretGit{remote: "origin", prBase: "main"}
}

// noLeak fails if out holds any test value.
func noLeak(t *testing.T, what, out string) {
	t.Helper()
	for _, v := range testValues {
		if strings.Contains(out, v) {
			t.Fatalf("%s leaks a value:\n%s", what, out)
		}
	}
}

// noPlaintextOnDisk fails if a file below root holds a test value.
func noPlaintextOnDisk(t *testing.T, root string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		for _, v := range testValues {
			if bytes.Contains(b, []byte(v)) {
				t.Fatalf("plaintext value in %s", p)
			}
		}
		return nil
	})
}

func (f *secretFixture) read(t *testing.T, rel string) map[string]string {
	t.Helper()
	plain, err := sops.Decrypt(context.Background(), f.root, rel)
	if err != nil {
		t.Fatal(err)
	}
	m, err := sops.ParseManifest(plain)
	if err != nil {
		t.Fatal(err)
	}
	return m.Values
}

func TestSecretPath(t *testing.T) {
	resetSecretFlags(t)
	secretOpts.repo = t.TempDir()
	t.Setenv(EnvLab, "")
	run := func(arg, output string) (string, error) {
		var out bytes.Buffer
		err := runSecretPath(arg, output, &out)
		return strings.TrimSpace(out.String()), err
	}
	secretOpts.lab = "labul"
	if got, err := run("kv/machinery-dev/argocd-oidc", ""); err != nil || got != "secrets/labul/kv/machinery-dev/argocd-oidc.enc.yaml" {
		t.Fatalf("vault->file = %q, %v", got, err)
	}
	if got, err := run("kv/machinery-dev/", ""); err != nil || got != "secrets/labul/kv/machinery-dev/" {
		t.Fatalf("prefix = %q, %v", got, err)
	}
	secretOpts.lab = ""
	if got, err := run("secrets/labul/kv/machinery-dev/argocd-oidc.enc.yaml", ""); err != nil || got != "kv/machinery-dev/argocd-oidc" {
		t.Fatalf("file->vault = %q, %v", got, err)
	}
	if got, err := run("secrets/labda/kv/app", ""); err != nil || got != "kv/app/" {
		t.Fatalf("dir->prefix = %q, %v", got, err)
	}
	out, err := run("secrets/labul/kv/App_Creds.enc.yaml", "json")
	if err != nil {
		t.Fatal(err)
	}
	var info secretPathInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatal(err)
	}
	if info.Lab != "labul" || info.VaultPath != "kv/App_Creds" || !strings.HasPrefix(info.SecretName, "labul.kv.app-creds--") || info.Namespace != "vault-sync" {
		t.Fatalf("json = %+v", info)
	}
	// A Vault path without a lab is a usage error.
	if _, err := run("kv/x", ""); exitCode(err) != exitCodeUsage {
		t.Fatalf("no lab: %v", err)
	}
	t.Setenv(EnvLab, "labda")
	if got, _ := run("kv/x", ""); got != "secrets/labda/kv/x.enc.yaml" {
		t.Fatalf("$CLAIMS_LAB: %q", got)
	}
}

func TestSecretSetViewUnset(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	secretOpts.lab = "labul"
	rel := "secrets/labul/kv/app/db.enc.yaml"
	valFile := filepath.Join(t.TempDir(), "v")
	if err := os.WriteFile(valFile, []byte(tvFromFile), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := runSecretSet(ctx, "kv/app/db", []string{"user=" + tvUser, "cert=@" + valFile, "token"}, strings.NewReader(tvStdin+"\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	noLeak(t, "set", out.String())
	if !strings.Contains(out.String(), "Created "+rel) {
		t.Fatalf("set output:\n%s", out.String())
	}
	got := f.read(t, rel)
	if got["user"] != tvUser || got["cert"] != tvFromFile || got["token"] != tvStdin {
		t.Fatal("values not stored as given")
	}
	plain, _ := sops.Decrypt(ctx, f.root, rel)
	m, _ := sops.ParseManifest(plain)
	if m.Name() != "labul.kv.app.db" || m.Namespace() != "vault-sync" {
		t.Fatalf("metadata = %s/%s", m.Namespace(), m.Name())
	}

	// --generate: created once, kept on the second run, replaced with --overwrite.
	secretSetFlags.generate, secretSetFlags.length, secretSetFlags.charset = true, 40, "hex"
	if err := runSecretSet(ctx, rel, []string{"password"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	gen := f.read(t, rel)["password"]
	if len(gen) != 40 || strings.Trim(gen, "0123456789abcdef") != "" {
		t.Fatalf("generated value has length %d / wrong charset", len(gen))
	}
	out.Reset()
	if err := runSecretSet(ctx, rel, []string{"password"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if f.read(t, rel)["password"] != gen || !strings.Contains(out.String(), "Unchanged: password") {
		t.Fatalf("generate-once broken:\n%s", out.String())
	}
	if strings.Contains(out.String(), gen) {
		t.Fatal("generated value printed")
	}
	secretSetFlags.overwrite = true
	if err := runSecretSet(ctx, rel, []string{"password"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if f.read(t, rel)["password"] == gen {
		t.Fatal("--overwrite did not regenerate")
	}
	secretSetFlags.generate, secretSetFlags.overwrite = false, false
	if err := runSecretSet(ctx, rel, []string{"x=1"}, nil, &out); err != nil {
		t.Fatal(err)
	}
	secretSetFlags.generate = true
	if err := runSecretSet(ctx, rel, []string{"x=1"}, nil, &out); exitCode(err) != exitCodeUsage {
		t.Fatalf("--generate with a value: %v", err)
	}
	secretSetFlags.generate = false

	// view: masked by default, the bare value with --reveal --key.
	out.Reset()
	if err := runSecretView(ctx, rel, &out); err != nil {
		t.Fatal(err)
	}
	noLeak(t, "view", out.String())
	for _, want := range []string{"user", "cert", "token", "password", sops.ShortHash(tvUser), "labul.kv.app.db", "kv/app/db"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("view lacks %q:\n%s", want, out.String())
		}
	}
	secretViewFlags.reveal, secretViewFlags.keys = true, []string{"user"}
	out.Reset()
	if err := runSecretView(ctx, "kv/app/db", &out); err != nil || out.String() != tvUser {
		t.Fatalf("reveal = %q, %v", out.String(), err)
	}
	secretViewFlags.reveal, secretViewFlags.keys = false, nil

	// unset
	out.Reset()
	if err := runSecretUnset(ctx, rel, []string{"cert", "token"}, &out); err != nil {
		t.Fatal(err)
	}
	if v := f.read(t, rel); len(v) != 3 || v["user"] != tvUser {
		t.Fatalf("unset left keys %v", sops.Keys(v))
	}
	if err := runSecretUnset(ctx, rel, []string{"nope"}, &out); err == nil {
		t.Fatal("missing key accepted")
	}
	if err := runSecretUnset(ctx, rel, []string{"user", "password", "x"}, &out); err == nil || !strings.Contains(err.Error(), "without keys") {
		t.Fatalf("emptying the file: %v", err)
	}

	// rotate keeps the values.
	before := f.read(t, rel)
	out.Reset()
	if err := runSecretRotate(ctx, []string{"secrets/labul/"}, &out); err != nil {
		t.Fatal(err)
	}
	if !sops.SameValues(before, f.read(t, rel)) || !strings.Contains(out.String(), "Rotated "+rel) {
		t.Fatalf("rotate:\n%s", out.String())
	}
	noPlaintextOnDisk(t, f.root)
}

func TestSecretSetNoRule(t *testing.T) {
	f := newSecretFixture(t)
	secretOpts.lab = "labxx"
	err := runSecretSet(context.Background(), "kv/app/db", []string{"a=" + tvUser}, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no creation rule") {
		t.Fatalf("err = %v", err)
	}
	noLeak(t, "error", err.Error())
	if _, err := os.Stat(filepath.Join(f.root, "secrets", "labxx")); err == nil {
		t.Fatal("wrote a file without a rule")
	}
}

func TestSecretSyncAndDiff(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	f.vault.Put("kv/app/db", map[string]any{"user": tvUser, "password": tvPassword})
	f.vault.Put("kv/app/api", map[string]any{"key": tvAPIKey})
	f.vault.Put("kv/kubeconfigs/c1", map[string]any{"kubeconfig": "derived-not-for-git"})
	f.vault.Put("kv/app/numbers", map[string]any{"port": 5432})
	secretOpts.lab = "labul"

	// dry run writes nothing
	secretSyncFlags.dryRun = true
	var out bytes.Buffer
	if err := runSyncVaultToSops(ctx, []string{"kv/"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "secrets/labul/kv/app/db.enc.yaml")); err == nil {
		t.Fatal("dry run wrote a file")
	}
	secretSyncFlags.dryRun = false

	out.Reset()
	if err := runSyncVaultToSops(ctx, []string{"kv/"}, &out); err != nil {
		t.Fatal(err)
	}
	noLeak(t, "sync", out.String())
	s := out.String()
	for _, want := range []string{"create    kv/app/api", "create    kv/app/db", "skip      kv/app/numbers", "1 derived path(s) excluded", "Wrote 2 files"} {
		if !strings.Contains(s, want) {
			t.Fatalf("sync output lacks %q:\n%s", want, s)
		}
	}
	if v := f.read(t, "secrets/labul/kv/app/db.enc.yaml"); v["password"] != tvPassword || v["user"] != tvUser {
		t.Fatal("db not synced")
	}
	if _, err := os.Stat(filepath.Join(f.root, "secrets/labul/kv/kubeconfigs")); err == nil {
		t.Fatal("derived secret written to git")
	}
	// Naming a derived path explicitly is refused.
	if err := runSyncVaultToSops(ctx, []string{"kv/kubeconfigs/c1"}, &out); err == nil || !strings.Contains(err.Error(), "derived") {
		t.Fatalf("derived explicit: %v", err)
	}

	// Equal now (once the unsyncable entry is gone): diff exits 0, a
	// second sync changes nothing.
	out.Reset()
	if err := runSecretDiff(ctx, "kv/app/", &out); !strings.Contains(out.String(), "values are not all strings") || exitCode(err) != exitCodeDrift {
		t.Fatalf("non-string entry: %v\n%s", err, out.String())
	}
	f.vault.Delete("kv/app/numbers")
	out.Reset()
	if err := runSecretDiff(ctx, "secrets/labul/kv/app/", &out); err != nil {
		t.Fatalf("diff: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := runSyncVaultToSops(ctx, []string{"kv/app/db"}, &out); err != nil || !strings.Contains(out.String(), "unchanged") {
		t.Fatalf("resync: %v\n%s", err, out.String())
	}

	// Drift: changed value, Vault-only and git-only paths.
	f.vault.Put("kv/app/db", map[string]any{"user": tvUser, "password": tvChanged})
	f.vault.Put("kv/app/new", map[string]any{"k": tvChanged})
	if err := runSecretSet(ctx, "kv/app/gitonly", []string{"k=" + tvChanged}, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := runSecretDiff(ctx, "kv/app/", &out)
	var de *driftError
	if !errors.As(err, &de) || exitCode(err) != exitCodeDrift || de.n != 3 {
		t.Fatalf("drift err = %v\n%s", err, out.String())
	}
	noLeak(t, "diff", out.String())
	for _, want := range []string{"~      kv/app/db", "changed    password", "+vault kv/app/new", "+git   kv/app/gitonly", "=      kv/app/api"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("diff lacks %q:\n%s", want, out.String())
		}
	}
	secretDiffFlags.output = "json"
	out.Reset()
	_ = runSecretDiff(ctx, "kv/app/db", &out)
	var rep diffReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil || len(rep.Paths) != 1 || rep.Paths[0].State != stateDifferent {
		t.Fatalf("json = %s (%v)", out.String(), err)
	}
	noLeak(t, "diff json", out.String())
	secretDiffFlags.output = ""

	// vault-to-sops: a conflict writes nothing without --overwrite.
	if err := runSyncVaultToSops(ctx, []string{"kv/app/db"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("conflict: %v", err)
	}
	if f.read(t, "secrets/labul/kv/app/db.enc.yaml")["password"] != tvPassword {
		t.Fatal("conflict overwrote the file")
	}
	secretSyncFlags.overwrite = true
	if err := runSyncVaultToSops(ctx, []string{"kv/app/db"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if f.read(t, "secrets/labul/kv/app/db.enc.yaml")["password"] != tvChanged {
		t.Fatal("--overwrite did not update")
	}

	// sops-to-vault: create the git-only entry; conflicts need --force.
	f.vault.Put("kv/app/api", map[string]any{"key": "other"})
	if err := runSyncSopsToVault(ctx, []string{"secrets/labul/kv/app/"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("s2v conflict: %v", err)
	}
	writes := f.vault.Writes()
	secretSyncS2VFlags.force = true
	out.Reset()
	if err := runSyncSopsToVault(ctx, []string{"secrets/labul/kv/app/"}, &out); err != nil {
		t.Fatal(err)
	}
	noLeak(t, "s2v", out.String())
	if f.vault.Get("kv/app/gitonly")["k"] != tvChanged || f.vault.Get("kv/app/api")["key"] != tvAPIKey || f.vault.Writes() != writes+2 {
		t.Fatalf("s2v did not write:\n%s", out.String())
	}
	noPlaintextOnDisk(t, f.root)
}

func TestSecretSyncCommitsBranch(t *testing.T) {
	f := newSecretFixture(t)
	f.vault.Put("kv/app/db", map[string]any{"password": tvPassword})
	secretOpts.lab = "labul"
	secretSyncFlags.git.branch = "secret/test-import"
	skipPreflight = true
	t.Setenv("GIT_TOKEN", "test-token")
	var out bytes.Buffer
	if err := runSyncVaultToSops(context.Background(), []string{"kv/"}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	noLeak(t, "sync", out.String())
	repo, err := git.PlainOpen(f.remote)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := repo.Reference(plumbing.NewBranchReferenceName("secret/test-import"), true)
	if err != nil {
		t.Fatalf("branch not pushed: %v", err)
	}
	c, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatal(err)
	}
	noLeak(t, "commit message", c.Message)
	if !strings.Contains(c.Message, "secrets/labul/kv/app/db.enc.yaml") || strings.Contains(c.Message, sops.ShortHash(tvPassword)) {
		t.Fatalf("commit message:\n%s", c.Message)
	}
	file, err := c.File("secrets/labul/kv/app/db.enc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := file.Contents()
	noLeak(t, "committed file", content)
	if !strings.Contains(content, "ENC[AES256_GCM") {
		t.Fatal("committed file is not encrypted")
	}
}

func TestDoctorSecrets(t *testing.T) {
	f := newSecretFixture(t)
	ctx := context.Background()
	if err := runSecretSet(ctx, "secrets/labul/kv/app/db.enc.yaml", []string{"a=" + tvUser}, nil, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	r := &preflightReport{}
	doctorSecretFlags.secret = "secrets/labul/kv/app/db.enc.yaml"
	checkSecrets(ctx, r, f.root)
	var out bytes.Buffer
	r.Print(&out, true)
	if r.Problems() != 0 {
		t.Fatalf("problems:\n%s", out.String())
	}
	for _, want := range []string{"sops", "age key", "rule 0", "recovery", "your key is a recipient", "matches .sops.yaml", "ttl 1h0m0s", "vault caps"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("doctor lacks %q:\n%s", want, out.String())
		}
	}

	// labda: our key is not a recipient; a wrong recovery recipient fails.
	r = &preflightReport{}
	doctorSecretFlags.secret = ""
	secretOpts.lab = "labda"
	doctorSecretFlags.recovery = "age1notarecipient"
	doctorSecretFlags.noVault = true
	checkSecrets(ctx, r, f.root)
	out.Reset()
	r.Print(&out, true)
	if r.Problems() != 2 || !strings.Contains(out.String(), "not a recipient") || !strings.Contains(out.String(), "lacks the recovery recipient") {
		t.Fatalf("labda:\n%s", out.String())
	}
}
