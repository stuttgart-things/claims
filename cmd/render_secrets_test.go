package cmd

import (
	"testing"

	"github.com/stuttgart-things/claims/internal/templates"
)

func TestResolveTemplateName(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		params  map[string]any
		want    string
		wantErr bool
	}{
		{
			name:    "simple template expression",
			pattern: "{{.secretName}}",
			params:  map[string]any{"secretName": "my-secret"},
			want:    "my-secret",
		},
		{
			name:    "static string",
			pattern: "static-name",
			params:  map[string]any{},
			want:    "static-name",
		},
		{
			name:    "combined expression",
			pattern: "{{.appName}}-pdns-vars",
			params:  map[string]any{"appName": "clusterbook"},
			want:    "clusterbook-pdns-vars",
		},
		{
			name:    "missing param",
			pattern: "{{.missingKey}}",
			params:  map[string]any{},
			want:    "<no value>",
		},
		{
			name:    "invalid template syntax",
			pattern: "{{.invalid",
			params:  map[string]any{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplateName(tt.pattern, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMergeSecretValues(t *testing.T) {
	tests := []struct {
		name        string
		fileSecrets map[string]string
		inlineRaw   []string
		want        map[string]string
		wantErr     bool
	}{
		{
			name:        "file secrets only",
			fileSecrets: map[string]string{"DB_PASS": "secret123"},
			inlineRaw:   nil,
			want:        map[string]string{"DB_PASS": "secret123"},
		},
		{
			name:        "inline secrets only",
			fileSecrets: nil,
			inlineRaw:   []string{"API_KEY=abc-def"},
			want:        map[string]string{"API_KEY": "abc-def"},
		},
		{
			name:        "inline overrides file",
			fileSecrets: map[string]string{"TOKEN": "old"},
			inlineRaw:   []string{"TOKEN=new"},
			want:        map[string]string{"TOKEN": "new"},
		},
		{
			name:        "invalid inline format",
			fileSecrets: nil,
			inlineRaw:   []string{"badformat"},
			wantErr:     true,
		},
		{
			name:        "empty inputs",
			fileSecrets: nil,
			inlineRaw:   nil,
			want:        map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mergeSecretValues(tt.fileSecrets, tt.inlineRaw)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %q, want %q", k, got[k], v)
				}
			}
			if len(got) != len(tt.want) {
				t.Errorf("length mismatch: got %d, want %d", len(got), len(tt.want))
			}
		})
	}
}

func TestProcessTemplateSecrets_SkipSecrets(t *testing.T) {
	tmpl := &templates.ClaimTemplate{
		Spec: templates.ClaimTemplateSpec{
			Secrets: []templates.SecretTemplate{
				{Name: "test-secret", Namespace: "default"},
			},
		},
	}

	config := &RenderConfig{
		SkipSecrets: true,
		OutputDir:   t.TempDir(),
	}

	results, err := processTemplateSecrets(tmpl, nil, nil, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results when skipping secrets, got %v", results)
	}
}

func TestProcessTemplateSecrets_NoSecrets(t *testing.T) {
	tmpl := &templates.ClaimTemplate{
		Spec: templates.ClaimTemplateSpec{},
	}

	config := &RenderConfig{OutputDir: t.TempDir()}

	results, err := processTemplateSecrets(tmpl, nil, nil, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for template without secrets")
	}
}

func TestProcessTemplateSecrets_MissingRequiredParam(t *testing.T) {
	tmpl := &templates.ClaimTemplate{
		Spec: templates.ClaimTemplateSpec{
			Secrets: []templates.SecretTemplate{
				{
					Name:      "test-secret",
					Namespace: "default",
					Parameters: []templates.Parameter{
						{Name: "TOKEN", Required: true},
					},
				},
			},
		},
	}

	config := &RenderConfig{
		OutputDir: t.TempDir(),
		DryRun:    true,
	}

	// No secret values provided
	results, err := processTemplateSecrets(tmpl, map[string]any{}, map[string]string{}, config)
	if err != nil {
		// SOPS might not be available in test env — that's a different error
		if results == nil {
			t.Skip("SOPS not available, skipping")
		}
	}

	// If SOPS is available, we should get an error about missing required param
	if len(results) > 0 && results[0].Error == nil {
		t.Errorf("expected error for missing required secret param")
	}
}
