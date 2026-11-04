package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"

	"github.com/stuttgart-things/claims/internal/layout"
	"github.com/stuttgart-things/claims/internal/params"
	"github.com/stuttgart-things/claims/internal/templates"
)

// testdata/source-path/<case>/rendered.yaml is what xr-native-vm 0.2.0
// renders for create-vm's form values (name snapshot, size large,
// build_source create-vm); want/ holds the files create-vm writes for the
// same order -- the snapshot hack/testdata/create-vm/<case>.yaml of
// stuttgart-things and, for dev, its render-monitoring step's ScrapeConfig.

// semantic parses a YAML stream; the target-path annotation is dropped, it
// is the layout's instruction (stuttgart-things hack/check-vm-kcl.py
// declares the same delta).
func semantic(t *testing.T, content string) []interface{} {
	t.Helper()
	var out []interface{}
	dec := yaml.NewDecoder(strings.NewReader(content))
	for {
		var d map[string]interface{}
		if err := dec.Decode(&d); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("parsing: %v", err)
		}
		if d == nil {
			continue
		}
		if md, ok := d["metadata"].(map[string]interface{}); ok {
			if ann, ok := md["annotations"].(map[string]interface{}); ok {
				delete(ann, layout.AnnotationTargetPath)
				if len(ann) == 0 {
					delete(md, "annotations")
				}
			}
		}
		out = append(out, d)
	}
	return out
}

func walk(t *testing.T, root string) []string {
	t.Helper()
	var rel []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && !strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			r, _ := filepath.Rel(root, p)
			rel = append(rel, filepath.ToSlash(r))
		}
		return nil
	})
	sort.Strings(rel)
	return rel
}

func vmTemplate() *templates.ClaimTemplate {
	return &templates.ClaimTemplate{
		Metadata: templates.ClaimTemplateMetadata{Name: "native-vm-labda-vsphere"},
		Spec: templates.ClaimTemplateSpec{
			Type: "xr-native-vm",
			Parameters: []templates.Parameter{
				{Name: "templateName", Default: "labda-vsphere"},
				{Name: "name", Default: "demo-vm"},
				{Name: "use_case", Default: "baseos"},
				{Name: "size", Default: "medium"},
			},
		},
	}
}

func caseOrders(t *testing.T, c string, p map[string]interface{}) []layoutOrder {
	t.Helper()
	rendered, err := os.ReadFile(filepath.Join("testdata/source-path", c, "rendered.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	results := []RenderResult{{TemplateName: "native-vm-labda-vsphere", ResourceName: "snapshot", Content: string(rendered)}}
	tps := []params.TemplateParams{{Name: "native-vm-labda-vsphere", Parameters: p}}
	orders, err := planLayout(results, tps, map[string]*templates.ClaimTemplate{"native-vm-labda-vsphere": vmTemplate()})
	if err != nil {
		t.Fatalf("planLayout: %v", err)
	}
	return orders
}

// TestSourcePathLayoutMatchesCreateVM is claims#92's acceptance test: the
// order lands in the files create-vm writes, with their content.
func TestSourcePathLayoutMatchesCreateVM(t *testing.T) {
	for _, c := range []string{"labda-vsphere-baseos", "labda-vsphere-dev"} {
		t.Run(c, func(t *testing.T) {
			root := t.TempDir()
			uc := strings.TrimPrefix(c, "labda-vsphere-")
			orders := caseOrders(t, c, map[string]interface{}{"name": "snapshot", "size": "large", "use_case": uc})
			config := &RenderConfig{OutputDir: root, Layout: LayoutSourcePath}
			if err := runSourcePathLayout(orders, config); err != nil {
				t.Fatalf("runSourcePathLayout: %v", err)
			}

			wantRoot := filepath.Join("testdata/source-path", c, "want")
			want := walk(t, wantRoot)
			if got := walk(t, root); !reflect.DeepEqual(got, want) {
				t.Fatalf("files %v, want %v", got, want)
			}
			for _, f := range want {
				w, _ := os.ReadFile(filepath.Join(wantRoot, f))
				g, _ := os.ReadFile(filepath.Join(root, f))
				if sw, sg := semantic(t, string(w)), semantic(t, string(g)); !reflect.DeepEqual(sw, sg) {
					t.Errorf("%s differs from create-vm's\n--- create-vm\n%s\n--- claims\n%s", f, w, g)
				}
			}
		})
	}
}

func TestSourcePathConventionVM(t *testing.T) {
	orders := caseOrders(t, "labda-vsphere-dev", map[string]interface{}{"name": "snapshot", "use_case": "dev", "size": "large"})
	conv := conventionFor(orders)
	if conv.Branch != "vm-snapshot" {
		t.Errorf("branch %q", conv.Branch)
	}
	if want := "feat(machinery): VM snapshot (dev, labda-vsphere) (MERGE = BAU)"; conv.Title != want {
		t.Errorf("title %q, want %q", conv.Title, want)
	}
	for _, s := range []string{"`machinery`", "clusters/labda/vsphere/machinery-orders-xrs/README.md",
		"`clusters/labda/vsphere/sthings-infra/monitoring/host-snapshot.yaml`", "| Use case | `dev` |"} {
		if !strings.Contains(conv.Body, s) {
			t.Errorf("body lacks %s:\n%s", s, conv.Body)
		}
	}

	// The lab comes from the template's default when the order does not set it.
	orders = caseOrders(t, "labda-vsphere-baseos", map[string]interface{}{"name": "snapshot"})
	if conv := conventionFor(orders); conv.Title != "feat(machinery): VM snapshot (baseos, labda-vsphere) (MERGE = BAU)" {
		t.Errorf("defaults: title %q", conv.Title)
	}
}

func TestSourcePathLayoutRefusesTakenName(t *testing.T) {
	root := t.TempDir()
	orders := caseOrders(t, "labda-vsphere-baseos", nil)
	p := filepath.Join(root, "clusters/labda/vsphere/machinery-orders-xrs/snapshot.yaml")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("taken"), 0o644)
	if err := runSourcePathLayout(orders, &RenderConfig{OutputDir: root, Layout: LayoutSourcePath}); err == nil {
		t.Fatal("want an error for an existing order file")
	}
}

func TestSourcePathLayoutDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	orders := caseOrders(t, "labda-vsphere-dev", nil)
	if err := runSourcePathLayout(orders, &RenderConfig{OutputDir: root, Layout: LayoutSourcePath, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if got := walk(t, root); len(got) != 0 {
		t.Errorf("dry run wrote %v", got)
	}
}

func TestSourcePathLayoutRefusesFailedRender(t *testing.T) {
	results := []RenderResult{{TemplateName: "x", Error: os.ErrInvalid}}
	if _, err := planLayout(results, nil, nil); err == nil {
		t.Fatal("want an error")
	}
}

// TestSourcePathLayoutCommits: a local repository gets the branch vm-<name>
// with one commit holding the order's files and the kustomization entry.
func TestSourcePathLayoutCommits(t *testing.T) {
	root := t.TempDir()
	repo, err := git.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	k := filepath.Join(root, "clusters/labda/vsphere/machinery-orders-xrs/kustomization.yaml")
	_ = os.MkdirAll(filepath.Dir(k), 0o755)
	_ = os.WriteFile(k, []byte("resources:\n  - other.yaml\n"), 0o644)
	wt, _ := repo.Worktree()
	_, _ = wt.Add(".")
	sig := &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()}
	if _, err := wt.Commit("init", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}

	orders := caseOrders(t, "labda-vsphere-dev", map[string]interface{}{"name": "snapshot", "use_case": "dev"})
	config := &RenderConfig{OutputDir: root, Layout: LayoutSourcePath, GitConfig: &GitConfig{Commit: true}}
	if err := runSourcePathLayout(orders, config); err != nil {
		t.Fatalf("runSourcePathLayout: %v", err)
	}

	head, _ := repo.Head()
	if head.Name().Short() != "vm-snapshot" {
		t.Errorf("branch %s, want vm-snapshot", head.Name().Short())
	}
	commit, _ := repo.CommitObject(head.Hash())
	if commit.Message != "feat(machinery): VM snapshot (dev, labda-vsphere) (MERGE = BAU)" {
		t.Errorf("commit message %q", commit.Message)
	}
	tree, _ := commit.Tree()
	for _, f := range []string{
		"clusters/labda/vsphere/machinery-orders-xrs/snapshot.yaml",
		"clusters/labda/vsphere/sthings-infra/monitoring/host-snapshot.yaml",
	} {
		if _, err := tree.File(f); err != nil {
			t.Errorf("%s not committed: %v", f, err)
		}
	}
	kf, _ := tree.File("clusters/labda/vsphere/machinery-orders-xrs/kustomization.yaml")
	if c, _ := kf.Contents(); c != "resources:\n  - other.yaml\n  - snapshot.yaml\n" {
		t.Errorf("kustomization committed as %q", c)
	}
	if _, err := os.Stat(filepath.Join(root, "claims/registry.yaml")); err == nil {
		t.Error("the source-path layout writes no registry")
	}
}

func TestValidateLayout(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  RenderConfig
		ok   bool
	}{
		{"classic", RenderConfig{}, true},
		{"source-path", RenderConfig{Layout: LayoutSourcePath}, true},
		{"unknown", RenderConfig{Layout: "machinery"}, false},
		{"interactive", RenderConfig{Layout: LayoutSourcePath, Interactive: true}, false},
		{"single-file", RenderConfig{Layout: LayoutSourcePath, SingleFile: true}, false},
		{"append", RenderConfig{Layout: LayoutSourcePath, FileMode: "append"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := c.cfg
			if err := validateLayout(&cfg); (err == nil) != c.ok {
				t.Errorf("validateLayout: %v, want ok=%v", err, c.ok)
			}
		})
	}
}
