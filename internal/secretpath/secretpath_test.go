package secretpath_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/secretpath"
)

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		lab, vault, file string
	}{
		{"labul", "kv/machinery-dev/argocd-oidc", "secrets/labul/kv/machinery-dev/argocd-oidc.enc.yaml"},
		{"labda", "secret/a", "secrets/labda/secret/a.enc.yaml"},
		{"labul", "kv/App_Creds/x.y", "secrets/labul/kv/App_Creds/x.y.enc.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.vault, func(t *testing.T) {
			r, err := secretpath.ParseVault(tt.lab, tt.vault)
			if err != nil {
				t.Fatal(err)
			}
			if r.File() != tt.file {
				t.Errorf("File() = %s, want %s", r.File(), tt.file)
			}
			back, err := secretpath.ParseFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			if back != r || back.VaultPath() != tt.vault {
				t.Errorf("ParseFile = %+v, want %+v", back, r)
			}
			if _, err := secretpath.ParseFile("./" + tt.file); err != nil {
				t.Errorf("./ prefix: %v", err)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, vp := range []string{"kv", "kv/", "kv//x", "kv/a/../b", "kv/data/x", "kv/metadata/x", ""} {
		if _, err := secretpath.ParseVault("labul", vp); err == nil {
			t.Errorf("ParseVault(%q): expected an error", vp)
		}
	}
	if _, err := secretpath.ParseVault("Lab UL", "kv/x"); err == nil {
		t.Error("invalid lab accepted")
	}
	for _, f := range []string{"secrets/labul/kv/x.yaml", "other/labul/kv/x.enc.yaml", "secrets/labul.enc.yaml", "secrets/labul/kv.enc.yaml"} {
		if _, err := secretpath.ParseFile(f); err == nil {
			t.Errorf("ParseFile(%q): expected an error", f)
		}
	}
}

func TestTree(t *testing.T) {
	tr, err := secretpath.ParseDir("secrets/labul/kv/machinery-dev")
	if err != nil {
		t.Fatal(err)
	}
	if tr.VaultPath() != "kv/machinery-dev/" || tr.Dir() != "secrets/labul/kv/machinery-dev" {
		t.Fatalf("tree = %+v", tr)
	}
	in, _ := secretpath.ParseVault("labul", "kv/machinery-dev/a/b")
	out, _ := secretpath.ParseVault("labul", "kv/machinery-devx/a")
	if !tr.Contains(in) || tr.Contains(out) {
		t.Fatal("Contains")
	}
	whole, err := secretpath.ParseVaultTree("labul", "kv/")
	if err != nil || whole.Prefix != "" || !whole.Contains(out) {
		t.Fatalf("mount tree = %+v, %v", whole, err)
	}
	if _, err := secretpath.ParseDir("secrets/labul"); err == nil {
		t.Fatal("a lab without mount is not a tree")
	}
}

var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

func TestSecretName(t *testing.T) {
	ref := func(lab, vp string) secretpath.Ref {
		r, err := secretpath.ParseVault(lab, vp)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if got := secretpath.SecretName(ref("labul", "kv/machinery-dev/argocd-oidc")); got != "labul.kv.machinery-dev.argocd-oidc" {
		t.Errorf("clean name = %s", got)
	}
	hashed := secretpath.SecretName(ref("labul", "kv/App_Creds"))
	if !strings.HasPrefix(hashed, "labul.kv.app-creds--") || len(hashed) != len("labul.kv.app-creds--")+10 {
		t.Errorf("hashed name = %s", hashed)
	}
	if secretpath.SecretName(ref("labul", "kv/App_Creds")) != hashed {
		t.Error("not deterministic")
	}

	// Paths that slug to the same text get different names, and no hashed
	// name equals a clean one.
	paths := []string{
		"kv/a/b", "kv/a.b", "kv/A/b", "kv/a_b", "kv/a-b", "kv/a--b", "kv/a/b-", "kv/a b",
		"kv/app-creds", "kv/App_Creds", "kv/app_creds", "kv/" + strings.Repeat("x", 64),
		"kv/" + strings.Repeat("segment/", 40) + "leaf",
	}
	seen := map[string]string{}
	for _, p := range paths {
		n := secretpath.SecretName(ref("labul", p))
		if prev, ok := seen[n]; ok {
			t.Errorf("collision: %s and %s -> %s", prev, p, n)
		}
		seen[n] = p
		if len(n) > 253 || !dns1123.MatchString(n) {
			t.Errorf("%s -> %q is not a DNS-1123 subdomain", p, n)
		}
	}
	// Labs keep names apart in a shared namespace.
	if secretpath.SecretName(ref("labul", "kv/x")) == secretpath.SecretName(ref("labda", "kv/x")) {
		t.Error("labs collide")
	}
}

func TestExcluder(t *testing.T) {
	e, err := secretpath.NewExcluder("kv/kubeconfigs", "kv/*/approle-secret-id", "pki**", "kv/certs/**/key")
	if err != nil {
		t.Fatal(err)
	}
	for vp, want := range map[string]bool{
		"kv/kubeconfigs":                true,
		"kv/kubeconfigs/cluster1":       true,
		"kv/kubeconfigsx":               false,
		"kv/app/approle-secret-id":      true,
		"kv/app/sub/approle-secret-id":  false,
		"pki_int/issue/x":               true,
		"kv/certs/a/b/key":              true,
		"kv/machinery-dev/argocd-oidc":  false,
		"/kv/kubeconfigs/with-slashes/": true,
	} {
		if _, got := e.Match(vp); got != want {
			t.Errorf("Match(%s) = %v, want %v", vp, got, want)
		}
	}
}
