package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stuttgart-things/claims/internal/registry"
)

func TestPrintTable(t *testing.T) {
	entries := []registry.ClaimEntry{
		{
			Name:      "my-vm",
			Template:  "vsphere-vm",
			Category:  "infra",
			Namespace: "default",
			Status:    "active",
			CreatedBy: "admin",
			Source:    "cli",
		},
		{
			Name:      "my-db",
			Template:  "postgres",
			Category:  "apps",
			Namespace: "production",
			Status:    "active",
			CreatedBy: "dev",
			Source:    "cli",
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printTable(entries)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	// Verify header columns
	headers := []string{"NAME", "TEMPLATE", "CATEGORY", "NAMESPACE", "STATUS", "CREATED BY", "SOURCE"}
	for _, h := range headers {
		if !strings.Contains(output, h) {
			t.Errorf("table output should contain header %q", h)
		}
	}

	// Verify data rows
	dataChecks := []string{"my-vm", "vsphere-vm", "infra", "my-db", "postgres", "apps", "production", "admin", "dev"}
	for _, d := range dataChecks {
		if !strings.Contains(output, d) {
			t.Errorf("table output should contain %q", d)
		}
	}
}

func TestPrintJSON(t *testing.T) {
	entries := []registry.ClaimEntry{
		{
			Name:      "my-vm",
			Template:  "vsphere-vm",
			Category:  "infra",
			Namespace: "default",
			Status:    "active",
			CreatedBy: "admin",
			Source:    "cli",
		},
		{
			Name:      "my-db",
			Template:  "postgres",
			Category:  "apps",
			Namespace: "production",
			Status:    "active",
			CreatedBy: "dev",
			Source:    "cli",
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printJSON(entries)

	_ = w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	output := buf.String()

	// Verify it is valid JSON
	var parsed []registry.ClaimEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	// Verify entries
	if len(parsed) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(parsed))
	}

	if parsed[0].Name != "my-vm" {
		t.Errorf("expected first entry name my-vm, got %s", parsed[0].Name)
	}
	if parsed[0].Template != "vsphere-vm" {
		t.Errorf("expected first entry template vsphere-vm, got %s", parsed[0].Template)
	}
	if parsed[0].Category != "infra" {
		t.Errorf("expected first entry category infra, got %s", parsed[0].Category)
	}

	if parsed[1].Name != "my-db" {
		t.Errorf("expected second entry name my-db, got %s", parsed[1].Name)
	}
	if parsed[1].Template != "postgres" {
		t.Errorf("expected second entry template postgres, got %s", parsed[1].Template)
	}
	if parsed[1].Namespace != "production" {
		t.Errorf("expected second entry namespace production, got %s", parsed[1].Namespace)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const listTestRegistry = `apiVersion: claim-registry.io/v1alpha1
kind: ClaimRegistry
claims:
  - name: flux-infra
    template: flux-gitrepository
    status: active
`

func listTestXR(name, file string) string {
	return `apiVersion: resources.stuttgart-things.com/v1alpha1
kind: NativeVsphereVM
metadata:
  name: ` + name + `
  annotations:
    stuttgart-things.com/source-path: ` + file + `
---
apiVersion: apiextensions.crossplane.io/v1beta1
kind: EnvironmentConfig
metadata:
  name: env
`
}

// listFixtureRepo builds a fleet repo: .git, optional registry, optional orders dir.
func listFixtureRepo(t *testing.T, withRegistry, withXRs bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withRegistry {
		writeFile(t, filepath.Join(root, "claims", "registry.yaml"), listTestRegistry)
	}
	if withXRs {
		f := "clusters/labda/vsphere/machinery-orders-xrs/demo.yaml"
		writeFile(t, filepath.Join(root, filepath.FromSlash(f)), listTestXR("demo-vm", f))
	}
	return root
}

func noEnv(string) string { return "" }

func TestResolveListLocation(t *testing.T) {
	repo := listFixtureRepo(t, true, true)
	sub := filepath.Join(repo, "clusters", "labda")
	outside := t.TempDir()
	custom := filepath.Join(outside, "my-registry.yaml")
	writeFile(t, custom, listTestRegistry)

	tests := []struct {
		name         string
		opts         listOptions
		wantRoot     string
		wantRegistry string
		wantExplicit bool
	}{
		{"default from a subdirectory", listOptions{Cwd: sub, Getenv: noEnv}, repo, filepath.Join(repo, "claims", "registry.yaml"), false},
		{"outside any repo", listOptions{Cwd: outside, Getenv: noEnv}, "", filepath.Join(outside, "claims", "registry.yaml"), false},
		{"--repo", listOptions{Repo: repo, Cwd: outside, Getenv: noEnv}, repo, filepath.Join(repo, "claims", "registry.yaml"), false},
		{"CLAIMS_REPO", listOptions{Cwd: outside, Getenv: func(k string) string {
			if k == envRepo {
				return repo
			}
			return ""
		}}, repo, filepath.Join(repo, "claims", "registry.yaml"), false},
		{"--registry absolute", listOptions{Registry: custom, Cwd: sub, Getenv: noEnv}, repo, custom, true},
		{"--registry relative to cwd", listOptions{Registry: "my-registry.yaml", Cwd: outside, Getenv: noEnv}, "", custom, true},
		{"--registry relative to repo", listOptions{Registry: "claims/registry.yaml", Cwd: sub, Getenv: noEnv}, repo, filepath.Join(repo, "claims", "registry.yaml"), true},
		{"CLAIMS_REGISTRY beats default", listOptions{Cwd: sub, Getenv: func(k string) string {
			if k == envRegistry {
				return custom
			}
			return ""
		}}, repo, custom, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := resolveListLocation(tt.opts)
			if loc.RepoRoot != tt.wantRoot || loc.RegistryPath != tt.wantRegistry || loc.RegistryExplicit != tt.wantExplicit {
				t.Errorf("got %+v, want root=%q registry=%q explicit=%v", loc, tt.wantRoot, tt.wantRegistry, tt.wantExplicit)
			}
		})
	}
}

func TestCollectClaims(t *testing.T) {
	names := func(es []registry.ClaimEntry) string {
		var out []string
		for _, e := range es {
			out = append(out, e.Name+"/"+e.Source)
		}
		return strings.Join(out, ",")
	}

	t.Run("registry and xrs", func(t *testing.T) {
		repo := listFixtureRepo(t, true, true)
		got, err := collectClaims(resolveListLocation(listOptions{Cwd: repo, Getenv: noEnv}), listSourceAll)
		if err != nil {
			t.Fatal(err)
		}
		if names(got) != "flux-infra/,demo-vm/xrs" {
			t.Errorf("got %s", names(got))
		}
		xr := got[1]
		if xr.Template != "NativeVsphereVM" || xr.Path != "clusters/labda/vsphere/machinery-orders-xrs/demo.yaml" || xr.Namespace != "default" {
			t.Errorf("xr entry = %+v", xr)
		}
	})

	t.Run("source filter", func(t *testing.T) {
		repo := listFixtureRepo(t, true, true)
		loc := resolveListLocation(listOptions{Cwd: repo, Getenv: noEnv})
		if got, _ := collectClaims(loc, listSourceXRs); names(got) != "demo-vm/xrs" {
			t.Errorf("xrs: got %s", names(got))
		}
		if got, _ := collectClaims(loc, listSourceRegistry); names(got) != "flux-infra/" {
			t.Errorf("registry: got %s", names(got))
		}
		if _, err := collectClaims(loc, "bogus"); err == nil {
			t.Error("unknown source: want error")
		}
	})

	t.Run("missing default registry is fine with xrs", func(t *testing.T) {
		repo := listFixtureRepo(t, false, true)
		got, err := collectClaims(resolveListLocation(listOptions{Cwd: repo, Getenv: noEnv}), listSourceAll)
		if err != nil || names(got) != "demo-vm/xrs" {
			t.Errorf("got %s, %v", names(got), err)
		}
	})

	t.Run("explicit missing registry is an error with hint", func(t *testing.T) {
		repo := listFixtureRepo(t, false, true)
		_, err := collectClaims(resolveListLocation(listOptions{Registry: "nope.yaml", Cwd: repo, Getenv: noEnv}), listSourceAll)
		if err == nil || !strings.Contains(err.Error(), "--registry") {
			t.Errorf("want error with hint, got %v", err)
		}
	})

	t.Run("outside a repo: error with hint", func(t *testing.T) {
		_, err := collectClaims(resolveListLocation(listOptions{Cwd: t.TempDir(), Getenv: noEnv}), listSourceAll)
		if err == nil || !strings.Contains(err.Error(), "CLAIMS_REPO") {
			t.Errorf("want error with hint, got %v", err)
		}
	})
}

func TestFilterKinds(t *testing.T) {
	entries := []registry.ClaimEntry{
		{Name: "flux-infra", Template: "flux-gitrepository", Source: "cli"},
		{Name: "dev7-vm", Template: "NativeVsphereVM", Source: sourceXRs},
		{Name: "runner-vm", Template: "NativeProxmoxVM", Source: sourceXRs},
		{Name: "app-dev", Template: "ClusterStack", Source: sourceXRs},
	}
	names := func(es []registry.ClaimEntry) string {
		var out []string
		for _, e := range es {
			out = append(out, e.Name)
		}
		return strings.Join(out, ",")
	}
	tests := []struct {
		name        string
		kinds       []string
		want        string
		wantDropped int
	}{
		{"no filter keeps all", nil, "flux-infra,dev7-vm,runner-vm,app-dev", 0},
		{"blank values ignored", []string{" ", ""}, "flux-infra,dev7-vm,runner-vm,app-dev", 0},
		{"one kind", []string{"NativeVsphereVM"}, "dev7-vm", 1},
		{"several, case-insensitive", []string{"nativevspherevm", " NATIVEPROXMOXVM "}, "dev7-vm,runner-vm", 1},
		{"unknown kind", []string{"Nope"}, "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dropped := filterKinds(entries, tt.kinds)
			if names(got) != tt.want || dropped != tt.wantDropped {
				t.Errorf("got %q dropped %d, want %q dropped %d", names(got), dropped, tt.want, tt.wantDropped)
			}
		})
	}
}

func TestWantRemoteList(t *testing.T) {
	fleet, _ := xrFixtureRepo(t)
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		opts   listOptions
		github string
		want   bool
	}{
		{"in the fleet checkout", listOptions{Cwd: fleet}, "", false},
		{"--github wins over a checkout", listOptions{Cwd: fleet}, "acme/fleet", true},
		{"outside any checkout", listOptions{Cwd: t.TempDir()}, "", true},
		{"in a checkout of another repository", listOptions{Cwd: other}, "", true},
		{"--repo names a local place", listOptions{Cwd: other, Repo: fleet}, "", false},
		{"--registry names a local file", listOptions{Cwd: other, Registry: "r.yaml"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Getenv = func(string) string { return "" }
			if got := wantRemoteList(resolveListLocation(tc.opts), tc.github); got != tc.want {
				t.Errorf("wantRemoteList = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCollectClaimsRemote(t *testing.T) {
	file := fxDir + "/sbx.yaml"
	files := fstest.MapFS{
		file:                          {Data: []byte(fxVM("NativeProxmoxVM", "sbx-vm", file))},
		fxDir + "/kustomization.yaml": {Data: []byte("resources: []\n")},
	}
	loc := listLocation{Remote: &fleetSource{FS: files, Repo: "acme/fleet", Ref: "main"}, RegistryPath: defaultRegistryPath}

	got, err := collectClaims(loc, listSourceAll)
	if err != nil || len(got) != 1 || got[0].Name != "sbx-vm" || got[0].Path != file || got[0].Source != sourceXRs {
		t.Fatalf("xrs only: %+v, %v", got, err)
	}

	files[defaultRegistryPath] = &fstest.MapFile{Data: []byte("claims:\n  - name: flux-infra\n    template: flux-gitrepository\n")}
	got, err = collectClaims(loc, listSourceAll)
	if err != nil || len(got) != 2 || got[0].Name != "flux-infra" {
		t.Fatalf("registry + xrs: %+v, %v", got, err)
	}

	empty := listLocation{Remote: &fleetSource{FS: fstest.MapFS{}, Repo: "acme/empty", Ref: "main"}}
	if _, err := collectClaims(empty, listSourceAll); err == nil || !strings.Contains(err.Error(), "github.com/acme/empty@main") {
		t.Errorf("empty remote: %v", err)
	}
}
