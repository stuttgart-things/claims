package params

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile_SingleTemplateYAML(t *testing.T) {
	content := `template: vsphere-vm
parameters:
  name: my-vm
  cpu: 4
  memory: 8Gi
`
	tmpFile := createTempFile(t, "params-single.yaml", content)
	defer os.Remove(tmpFile)

	pf, err := ParseFile(tmpFile)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if len(pf.Templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(pf.Templates))
	}

	if pf.Templates[0].Name != "vsphere-vm" {
		t.Errorf("expected template name 'vsphere-vm', got '%s'", pf.Templates[0].Name)
	}

	if pf.Templates[0].Parameters["name"] != "my-vm" {
		t.Errorf("expected name 'my-vm', got '%v'", pf.Templates[0].Parameters["name"])
	}

	if pf.Templates[0].Parameters["cpu"] != 4 {
		t.Errorf("expected cpu 4, got '%v'", pf.Templates[0].Parameters["cpu"])
	}
}

func TestParseFile_MultiTemplateYAML(t *testing.T) {
	content := `templates:
  - name: vsphere-vm
    parameters:
      name: my-vm
      cpu: 4

  - name: postgres-db
    parameters:
      name: my-database
      version: "15"
`
	tmpFile := createTempFile(t, "params-multi.yaml", content)
	defer os.Remove(tmpFile)

	pf, err := ParseFile(tmpFile)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if len(pf.Templates) != 2 {
		t.Errorf("expected 2 templates, got %d", len(pf.Templates))
	}

	if pf.Templates[0].Name != "vsphere-vm" {
		t.Errorf("expected first template 'vsphere-vm', got '%s'", pf.Templates[0].Name)
	}

	if pf.Templates[1].Name != "postgres-db" {
		t.Errorf("expected second template 'postgres-db', got '%s'", pf.Templates[1].Name)
	}
}

func TestParseFile_JSON(t *testing.T) {
	content := `{
  "template": "vsphere-vm",
  "parameters": {
    "name": "my-vm",
    "cpu": 4
  }
}`
	tmpFile := createTempFile(t, "params.json", content)
	defer os.Remove(tmpFile)

	pf, err := ParseFile(tmpFile)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if len(pf.Templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(pf.Templates))
	}

	if pf.Templates[0].Name != "vsphere-vm" {
		t.Errorf("expected template name 'vsphere-vm', got '%s'", pf.Templates[0].Name)
	}
}

func TestParseFile_UnknownExtension(t *testing.T) {
	// YAML content with unknown extension - should try both parsers
	content := `template: vsphere-vm
parameters:
  name: my-vm
`
	tmpFile := createTempFile(t, "params.txt", content)
	defer os.Remove(tmpFile)

	pf, err := ParseFile(tmpFile)
	if err != nil {
		t.Fatalf("ParseFile() error = %v", err)
	}

	if len(pf.Templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(pf.Templates))
	}
}

func TestParseFile_NotFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/path/params.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestParseInlineParams(t *testing.T) {
	tests := []struct {
		name    string
		params  []string
		want    map[string]any
		wantErr bool
	}{
		{
			name:   "single param",
			params: []string{"name=my-vm"},
			want:   map[string]any{"name": "my-vm"},
		},
		{
			name:   "multiple params",
			params: []string{"name=my-vm", "cpu=4", "memory=8Gi"},
			want:   map[string]any{"name": "my-vm", "cpu": "4", "memory": "8Gi"},
		},
		{
			name:   "value with equals sign",
			params: []string{"command=echo hello=world"},
			want:   map[string]any{"command": "echo hello=world"},
		},
		{
			name:   "empty value",
			params: []string{"name="},
			want:   map[string]any{"name": ""},
		},
		{
			name:    "invalid format",
			params:  []string{"name"},
			wantErr: true,
		},
		{
			name:   "empty slice",
			params: []string{},
			want:   map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseInlineParams(tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseInlineParams() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				for k, v := range tt.want {
					if got[k] != v {
						t.Errorf("ParseInlineParams()[%s] = %v, want %v", k, got[k], v)
					}
				}
			}
		})
	}
}

func TestMergeParams(t *testing.T) {
	fileParams := map[string]any{
		"name":   "file-name",
		"cpu":    4,
		"memory": "8Gi",
	}

	inlineParams := map[string]any{
		"cpu":      8,
		"newparam": "value",
	}

	result := MergeParams(fileParams, inlineParams)

	// Check that inline params override file params
	if result["cpu"] != 8 {
		t.Errorf("expected cpu=8, got %v", result["cpu"])
	}

	// Check that file-only params are preserved
	if result["name"] != "file-name" {
		t.Errorf("expected name='file-name', got %v", result["name"])
	}

	if result["memory"] != "8Gi" {
		t.Errorf("expected memory='8Gi', got %v", result["memory"])
	}

	// Check that inline-only params are added
	if result["newparam"] != "value" {
		t.Errorf("expected newparam='value', got %v", result["newparam"])
	}
}

func TestParameterFile_Normalize(t *testing.T) {
	pf := &ParameterFile{
		Template: "vsphere-vm",
		Parameters: map[string]any{
			"name": "my-vm",
		},
	}

	pf.Normalize()

	if len(pf.Templates) != 1 {
		t.Errorf("expected 1 template after normalize, got %d", len(pf.Templates))
	}

	if pf.Templates[0].Name != "vsphere-vm" {
		t.Errorf("expected template name 'vsphere-vm', got '%s'", pf.Templates[0].Name)
	}
}

func TestParameterFile_Normalize_NoOp(t *testing.T) {
	// Already in multi-template format - normalize should be a no-op
	pf := &ParameterFile{
		Templates: []TemplateParams{
			{Name: "tmpl1", Parameters: map[string]any{"key": "val"}},
		},
	}

	pf.Normalize()

	if len(pf.Templates) != 1 {
		t.Errorf("expected 1 template after normalize, got %d", len(pf.Templates))
	}
}

func createTempFile(t *testing.T, name, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, name)
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	return tmpFile
}
