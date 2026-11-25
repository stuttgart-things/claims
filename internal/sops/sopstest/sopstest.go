// Package sopstest sets up throwaway age keys and a .sops.yaml for tests.
// No real key or secret ever appears in a test.
package sopstest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// Keys are throwaway age identities: one per lab plus a recovery key.
type Keys struct {
	Lab      map[string]*age.X25519Identity
	Recovery *age.X25519Identity
}

// RequireSOPS skips the test without a sops binary.
func RequireSOPS(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sops"); err != nil {
		t.Skip("sops not installed")
	}
}

// NewKeys generates identities for labs and a recovery key.
func NewKeys(t *testing.T, labs ...string) *Keys {
	t.Helper()
	k := &Keys{Lab: map[string]*age.X25519Identity{}}
	for _, l := range labs {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		k.Lab[l] = id
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	k.Recovery = id
	return k
}

// WriteConfig writes <root>/.sops.yaml with one rule per lab (lab key and
// recovery key, encrypted_regex ^(data|stringData)$), as in docs/13-secrets.md.
func (k *Keys) WriteConfig(t *testing.T, root string, labs ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("creation_rules:\n")
	for _, l := range labs {
		fmt.Fprintf(&b, "  - path_regex: ^secrets/%s/.*\\.enc\\.yaml$\n", l)
		b.WriteString("    encrypted_regex: ^(data|stringData)$\n")
		fmt.Fprintf(&b, "    age: >-\n      %s,\n      %s\n", k.Lab[l].Recipient(), k.Recovery.Recipient())
	}
	if err := os.WriteFile(filepath.Join(root, ".sops.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// UseKey points SOPS_AGE_KEY_FILE at a file holding id (0600, in the test's
// temp dir) and clears the other SOPS_AGE_* variables.
func UseKey(t *testing.T, id *age.X25519Identity) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(f, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	Unsetenv(t, "SOPS_AGE_KEY", "SOPS_AGE_KEY_CMD")
	t.Setenv("SOPS_AGE_KEY_FILE", f)
}

// Unsetenv removes variables for the test and restores them afterwards.
// (sops treats a set but empty SOPS_AGE_KEY as a key and panics on it, so
// t.Setenv(k, "") is not enough.)
func Unsetenv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "") // registers the restore
		_ = os.Unsetenv(k)
	}
}
