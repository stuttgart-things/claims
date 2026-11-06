package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/templates"
)

func devResult(t *testing.T) RenderResult {
	t.Helper()
	rendered, err := os.ReadFile(filepath.Join("testdata/source-path", "labda-vsphere-dev", "rendered.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return RenderResult{
		TemplateName: "native-vm-labda-vsphere",
		ResourceName: "snapshot",
		Content:      string(rendered),
		Params:       map[string]interface{}{"name": "snapshot", "size": "large", "use_case": "dev"},
	}
}

func TestPlanInteractiveLayout(t *testing.T) {
	root := t.TempDir()
	vm := vmTemplate()
	withSecrets := vmTemplate()
	withSecrets.Spec.Secrets = []templates.SecretTemplate{{Name: "pw"}}
	lookup := map[string]*templates.ClaimTemplate{vm.Metadata.Name: vm}

	t.Run("VM order with source-path", func(t *testing.T) {
		l := planInteractiveLayoutIn(root, []RenderResult{devResult(t)}, lookup, &RenderConfig{})
		if l == nil {
			t.Fatal("want a layout")
		}
		var paths []string
		for _, f := range allFiles(l.Orders) {
			paths = append(paths, f.Path)
		}
		// the dev VM's ScrapeConfig goes to its target-path, apart from the order
		if len(paths) < 2 || !strings.Contains(strings.Join(paths, " "), "machinery-orders-xrs/snapshot.yaml") {
			t.Errorf("files = %v", paths)
		}
		s := layoutSummary(l, "main")
		for _, want := range []string{"Write:      clusters/labda/vsphere/machinery-orders-xrs/snapshot.yaml", "Branch:     vm-snapshot (from main)", "PR title:   feat(machinery): VM snapshot"} {
			if !strings.Contains(s, want) {
				t.Errorf("summary lacks %q:\n%s", want, s)
			}
		}
	})

	t.Run("no annotation", func(t *testing.T) {
		r := RenderResult{TemplateName: "cm", Content: "kind: ConfigMap\nmetadata:\n  name: x\n"}
		if l := planInteractiveLayoutIn(root, []RenderResult{r}, lookup, &RenderConfig{}); l != nil {
			t.Error("want no layout")
		}
	})

	t.Run("failed render", func(t *testing.T) {
		rs := []RenderResult{devResult(t), {TemplateName: "x", Error: errors.New("boom")}}
		if l := planInteractiveLayoutIn(root, rs, lookup, &RenderConfig{}); l != nil {
			t.Error("want no layout")
		}
	})

	t.Run("template with secrets", func(t *testing.T) {
		sl := map[string]*templates.ClaimTemplate{vm.Metadata.Name: withSecrets}
		if l := planInteractiveLayoutIn(root, []RenderResult{devResult(t)}, sl, &RenderConfig{}); l != nil {
			t.Error("want no layout with secrets")
		}
		if l := planInteractiveLayoutIn(root, []RenderResult{devResult(t)}, sl, &RenderConfig{SkipSecrets: true}); l == nil {
			t.Error("--skip-secrets: want a layout")
		}
	})
}
