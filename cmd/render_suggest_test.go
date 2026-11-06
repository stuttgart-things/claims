package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func orderYAML(name, sourcePath string) string {
	return `apiVersion: resources.stuttgart-things.com/v1alpha1
kind: NativeVsphereVM
metadata:
  name: ` + name + `-vm
  annotations:
    stuttgart-things.com/source-path: ` + sourcePath + `
---
apiVersion: apiextensions.crossplane.io/v1beta1
kind: EnvironmentConfig
metadata:
  name: dev-machine-` + name + `
`
}

func TestSuggestOutputFrom(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "clusters")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	const sp = "clusters/labda/vsphere/machinery-orders-xrs/demo.yaml"
	ordersRel := filepath.FromSlash("clusters/labda/vsphere/machinery-orders-xrs")

	tests := []struct {
		name    string
		results []RenderResult
		cwd     string
		want    outputSuggestion
	}{
		{"repo root", []RenderResult{{Content: orderYAML("demo", sp)}}, repo, outputSuggestion{Dir: ordersRel, File: "demo.yaml"}},
		{"from a subdirectory", []RenderResult{{Content: orderYAML("demo", sp)}}, sub, outputSuggestion{Dir: filepath.FromSlash("labda/vsphere/machinery-orders-xrs"), File: "demo.yaml"}},
		{"outside a repo", []RenderResult{{Content: orderYAML("demo", sp)}}, t.TempDir(), outputSuggestion{}},
		{"no annotation", []RenderResult{{Content: "kind: ConfigMap\nmetadata:\n  name: x\n"}}, repo, outputSuggestion{}},
		{"failed results ignored", []RenderResult{{Error: os.ErrNotExist}, {Content: orderYAML("demo", sp)}}, repo, outputSuggestion{Dir: ordersRel, File: "demo.yaml"}},
		{"results disagree", []RenderResult{
			{Content: orderYAML("a", "clusters/a-xrs/a.yaml")},
			{Content: orderYAML("b", "clusters/b-xrs/b.yaml")},
		}, repo, outputSuggestion{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suggestOutputFrom(tt.results, tt.cwd); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
