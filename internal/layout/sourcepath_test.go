package layout_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/layout"
)

const order = `apiVersion: v1
kind: XR
metadata:
  name: demo-vm
  annotations:
    stuttgart-things.com/source-path: orders/demo.yaml
---
kind: ScrapeConfig
metadata:
  name: host-demo
  annotations:
    stuttgart-things.com/target-path: monitoring/host-demo.yaml
---
# comments only
---
kind: EnvironmentConfig
metadata:
  name: dev-machine-demo
`

func TestPlan(t *testing.T) {
	files, err := layout.Plan(order)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if files[0].Path != "orders/demo.yaml" || len(files[0].Documents) != 2 {
		t.Errorf("order file: %s with %d documents, want orders/demo.yaml with XR + EnvironmentConfig", files[0].Path, len(files[0].Documents))
	}
	if files[0].Documents[1].Kind != "EnvironmentConfig" {
		t.Errorf("a document without a path after a target-path one belongs to the order file, got %s", files[0].Documents[1].Kind)
	}
	if files[1].Path != "monitoring/host-demo.yaml" || files[1].Documents[0].Kind != "ScrapeConfig" {
		t.Errorf("target-path file: %s", files[1].Path)
	}
	c := files[0].Content()
	if !strings.HasPrefix(c, "---\napiVersion: v1\n") || strings.Count(c, "---\n") != 2 {
		t.Errorf("content: %q", c)
	}
}

func TestPlanRefuses(t *testing.T) {
	cases := map[string]string{
		"no path":   "kind: XR\nmetadata:\n  name: x\n",
		"absolute":  "kind: XR\nmetadata:\n  annotations:\n    stuttgart-things.com/source-path: /etc/x.yaml\n",
		"escapes":   "kind: XR\nmetadata:\n  annotations:\n    stuttgart-things.com/source-path: ../x.yaml\n",
		"not clean": "kind: XR\nmetadata:\n  annotations:\n    stuttgart-things.com/source-path: a/./x.yaml\n",
		"not yaml":  "kind: XR\nmetadata:\n  annotations:\n    stuttgart-things.com/source-path: a/x.json\n",
		"empty":     "# nothing\n",
		"broken":    "kind: [\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := layout.Plan(in); err == nil {
				t.Errorf("want an error")
			}
		})
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func files(paths ...string) []layout.File {
	var fs []layout.File
	for _, p := range paths {
		fs = append(fs, layout.File{Path: p})
	}
	return fs
}

func TestKustomizations(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "xrs/kustomization.yaml", "---\napiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n    - a.yaml\n    - b.yaml")
	writeFile(t, root, "done/kustomization.yaml", "resources:\n  - demo.yaml\n")

	changes, err := layout.Kustomizations(root, files("xrs/demo.yaml", "xrs/second.yaml", "plain/demo.yaml", "done/demo.yaml"))
	if err != nil {
		t.Fatalf("Kustomizations: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("want one change (xrs), got %d", len(changes))
	}
	c := changes[0]
	want := "---\napiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n    - a.yaml\n    - b.yaml\n    - demo.yaml\n    - second.yaml\n"
	if c.Path != "xrs/kustomization.yaml" || c.After != want {
		t.Errorf("change %s:\n%s\nwant:\n%s", c.Path, c.After, want)
	}
	if len(c.Lines) != 2 || c.Lines[0] != "    - demo.yaml" {
		t.Errorf("lines: %q", c.Lines)
	}

	if err := layout.Write(root, nil, changes); err != nil {
		t.Fatalf("Write: %v", err)
	}
	again, err := layout.Kustomizations(root, files("xrs/demo.yaml"))
	if err != nil || len(again) != 0 {
		t.Errorf("appending twice: %v, %d changes, want none", err, len(again))
	}
}

func TestKustomizationsRefuses(t *testing.T) {
	cases := map[string]string{
		"resources not last": "resources:\n  - a.yaml\nnamespace: x\n",
		"flow list":          "resources: [a.yaml]\n",
		"empty list":         "resources: []\n",
		"no resources":       "kind: Kustomization\n",
	}
	for name, k := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "d/kustomization.yaml", k)
			if _, err := layout.Kustomizations(root, files("d/x.yaml")); err == nil {
				t.Errorf("want an error")
			}
		})
	}
}

func TestWriteRefusesExisting(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "orders/demo.yaml", "old")
	fs, err := layout.Plan(order)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Write(root, fs, nil); err == nil {
		t.Fatal("want an error for an existing file")
	}
	if _, err := os.Stat(filepath.Join(root, "monitoring/host-demo.yaml")); err == nil {
		t.Error("nothing may be written when one file exists")
	}
}
