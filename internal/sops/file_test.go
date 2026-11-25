package sops_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/sops"
	"github.com/stuttgart-things/claims/internal/sops/sopstest"
)

func TestConfigRuleFor(t *testing.T) {
	root := t.TempDir()
	keys := sopstest.NewKeys(t, "labul", "labda")
	keys.WriteConfig(t, root, "labul", "labda")
	cfg, err := sops.LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.RuleFor("secrets/labda/kv/x.enc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if r.Index != 1 || len(r.Age) != 2 || !r.Has(keys.Recovery.Recipient().String()) || r.EncryptedRegex != "^(data|stringData)$" {
		t.Fatalf("rule = %+v", r)
	}
	if _, err := cfg.RuleFor("secrets/labxx/kv/x.enc.yaml"); err == nil || !strings.Contains(err.Error(), "no creation rule") {
		t.Fatalf("unmatched: %v", err)
	}
	shared := cfg.SharedRecipients()
	if len(shared) != 1 || shared[0] != keys.Recovery.Recipient().String() {
		t.Fatalf("shared = %v", shared)
	}
}

func TestConfigKeyGroupsAndList(t *testing.T) {
	root := t.TempDir()
	data := `creation_rules:
  - path_regex: ^a/
    age: [age1one, "age1two,age1three"]
  - path_regex: ^b/
    key_groups:
      - age: [age1four]
  - path_regex: ^c/
`
	if err := os.WriteFile(filepath.Join(root, ".sops.yaml"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := sops.LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := cfg.RuleFor("a/x"); r == nil || len(r.Age) != 3 {
		t.Fatalf("list form: %+v", r)
	}
	if r, _ := cfg.RuleFor("b/x"); r == nil || len(r.Age) != 1 {
		t.Fatalf("key_groups: %+v", r)
	}
	if _, err := cfg.RuleFor("c/x"); err == nil || !strings.Contains(err.Error(), "no age recipients") {
		t.Fatalf("no recipients: %v", err)
	}
	if _, err := sops.LoadConfig(t.TempDir()); err == nil {
		t.Fatal("missing .sops.yaml must be an error")
	}
}

func TestManifest(t *testing.T) {
	m := sops.NewManifest("labul.kv.x", "vault-sync", map[string]string{sops.AnnotationVaultPath: "kv/x"})
	m.Values["b"] = "two"
	m.Values["a"] = "one"
	out, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := sops.ParseManifest(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name() != "labul.kv.x" || back.Namespace() != "vault-sync" || !sops.SameValues(back.Values, m.Values) {
		t.Fatalf("round trip: %+v", back)
	}
	// data (base64) is read; stringData wins.
	in := "apiVersion: v1\nkind: Secret\nmetadata: {name: n}\ndata:\n  a: b25l\n  c: dGhyZWU=\nstringData:\n  a: other\n"
	p, err := sops.ParseManifest([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if p.Values["a"] != "other" || p.Values["c"] != "three" {
		t.Fatalf("values = %v", p.Values)
	}
	// A YAML error never quotes the input.
	_, err = sops.ParseManifest([]byte("kind: Secret\nstringData:\n  a: [unclosed supersecretvalue\n"))
	if err == nil || strings.Contains(err.Error(), "supersecretvalue") {
		t.Fatalf("parse error = %v", err)
	}
	if _, err := sops.ParseManifest([]byte("kind: ConfigMap\n")); err == nil {
		t.Fatal("ConfigMap accepted")
	}
	if sops.ValidKey("a/b") == nil || sops.ValidKey("ok.key_1-x") != nil {
		t.Fatal("ValidKey")
	}
}

func TestCheckEncrypted(t *testing.T) {
	ok := "stringData:\n  a: ENC[AES256_GCM,data:x,type:str]\nsops:\n  version: 3.10.2\n"
	if err := sops.CheckEncrypted([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	bad := "stringData:\n  a: plaintext\nsops:\n  version: 3.10.2\n"
	if err := sops.CheckEncrypted([]byte(bad)); err == nil || strings.Contains(err.Error(), "plaintext") {
		t.Fatalf("bad = %v", err)
	}
	if err := sops.CheckEncrypted([]byte("stringData: {}\n")); err == nil {
		t.Fatal("no sops metadata accepted")
	}
}

func TestFindAgeKeys(t *testing.T) {
	keys := sopstest.NewKeys(t, "labul")
	sopstest.UseKey(t, keys.Lab["labul"])
	k, err := sops.FindAgeKeys()
	if err != nil {
		t.Fatal(err)
	}
	if ok, known := k.CanDecrypt([]string{"age1other", keys.Lab["labul"].Recipient().String()}); !ok || !known {
		t.Fatal("CanDecrypt")
	}
	if ok, _ := k.CanDecrypt([]string{keys.Recovery.Recipient().String()}); ok {
		t.Fatal("recovery key is not ours")
	}
	sopstest.Unsetenv(t, "SOPS_AGE_KEY_FILE")
	t.Setenv("SOPS_AGE_KEY", "not a key AGE-SECRET-KEY-1SECRETISH")
	if _, err := sops.FindAgeKeys(); err == nil || strings.Contains(err.Error(), "SECRETISH") {
		t.Fatalf("invalid key: %v", err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for v, want := range map[string]bool{"3.9.0": true, "3.10.2": true, "3.8.1": false, "4.0.0": true, "2.99.99": false} {
		if got := sops.VersionAtLeast(v, sops.MinVersion); got != want {
			t.Errorf("%s: %v", v, got)
		}
	}
}

// TestEncryptDecrypt runs the real sops with throwaway keys.
func TestEncryptDecrypt(t *testing.T) {
	sopstest.RequireSOPS(t)
	ctx := context.Background()
	root := t.TempDir()
	keys := sopstest.NewKeys(t, "labul")
	keys.WriteConfig(t, root, "labul")
	sopstest.UseKey(t, keys.Lab["labul"])

	v, err := sops.Version(ctx)
	if err != nil || !sops.VersionAtLeast(v, sops.MinVersion) {
		t.Skipf("sops %s too old: %v", v, err)
	}

	rel := "secrets/labul/kv/app/db.enc.yaml"
	m := sops.NewManifest("n", "vault-sync", nil)
	m.Values["password"] = "throwaway-test-value"
	plain, _ := m.Marshal()
	enc, err := sops.EncryptFor(ctx, root, rel, plain, m.SecretValues())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), "throwaway-test-value") {
		t.Fatal("plaintext in ciphertext")
	}
	if err := sops.CheckEncrypted(enc); err != nil {
		t.Fatal(err)
	}
	if got := sops.FileRecipients(enc); len(got) != 2 {
		t.Fatalf("recipients = %v", got)
	}
	abs := filepath.Join(root, rel)
	if err := sops.WriteFileAtomic(abs, enc); err != nil {
		t.Fatal(err)
	}
	out, err := sops.Decrypt(ctx, root, rel)
	if err != nil {
		t.Fatal(err)
	}
	back, err := sops.ParseManifest(out)
	if err != nil || back.Values["password"] != "throwaway-test-value" {
		t.Fatalf("decrypt: %v %v", back, err)
	}

	// The recovery key alone decrypts too.
	sopstest.UseKey(t, keys.Recovery)
	if _, err := sops.Decrypt(ctx, root, rel); err != nil {
		t.Fatalf("recovery key: %v", err)
	}
	if err := sops.UpdateKeys(ctx, root, rel); err != nil {
		t.Fatal(err)
	}
	if err := sops.Rotate(ctx, root, rel); err != nil {
		t.Fatal(err)
	}

	// No rule for the path: sops refuses, claims never guesses.
	if _, err := sops.EncryptFor(ctx, root, "secrets/labda/kv/x.enc.yaml", plain, m.SecretValues()); err == nil {
		t.Fatal("encrypted without a rule")
	}
}
