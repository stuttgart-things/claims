package conditions_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/conditions"
	"github.com/stuttgart-things/claims/internal/templates"
)

// vmTemplateJSON is claim-machinery-api's conditions_test.go template
// (xr-native-vm, kcl#396) as the API sends it.
const vmTemplateJSON = `{
  "metadata": {"name": "native-vm"},
  "spec": {"type": "xr-native-vm", "source": "oci://example/xr-native-vm", "parameters": [
    {"name": "name", "type": "string", "required": true, "pattern": "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"},
    {"name": "use_case", "type": "string", "default": "baseos", "enum": ["baseos", "kind", "dev", "rke2", "github-runner"]},
    {"name": "size", "type": "string", "default": "medium", "enum": ["small", "medium", "large", "xlarge"],
     "enumWhen": {"use_case": {"dev": ["large", "xlarge"], "github-runner": ["large", "xlarge"], "rke2": ["medium", "large", "xlarge"]}}},
    {"name": "kind_workers", "type": "string", "default": "1", "showWhen": {"use_case": ["kind"]}},
    {"name": "dev_features", "type": "array", "multiselect": true, "enum": ["docker", "go", "kind"], "default": ["docker"],
     "showWhen": {"use_case": ["dev"]}},
    {"name": "dev_kind_workers", "type": "string", "default": "3", "showWhen": {"dev_features": ["kind"]}},
    {"name": "runner_toolchain", "type": "boolean", "default": true, "showWhen": {"use_case": ["github-runner"]}},
    {"name": "runner_extra", "type": "string", "showWhen": {"runner_toolchain": ["true"]}}
  ]}
}`

func vmParams(t *testing.T) []templates.Parameter {
	t.Helper()
	var tmpl templates.ClaimTemplate
	if err := json.Unmarshal([]byte(vmTemplateJSON), &tmpl); err != nil {
		t.Fatal(err)
	}
	return tmpl.Spec.Parameters
}

func param(t *testing.T, params []templates.Parameter, name string) templates.Parameter {
	t.Helper()
	for _, p := range params {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no parameter %s", name)
	return templates.Parameter{}
}

func fieldsOf(t *testing.T, err error) conditions.FieldErrors {
	t.Helper()
	var verr *conditions.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want a ValidationError, got %v", err)
	}
	return verr.Fields
}

type in = map[string]any

func TestDecode(t *testing.T) {
	ps := vmParams(t)
	if got := param(t, ps, "size").EnumWhen["use_case"]["dev"]; !reflect.DeepEqual(got, []string{"large", "xlarge"}) {
		t.Errorf("enumWhen = %v", got)
	}
	if got := param(t, ps, "kind_workers").ShowWhen; !reflect.DeepEqual(got, map[string][]string{"use_case": {"kind"}}) {
		t.Errorf("showWhen = %v", got)
	}
	if !conditions.HasConditions(ps) {
		t.Error("HasConditions = false")
	}
	if conditions.HasConditions([]templates.Parameter{{Name: "a", Enum: []string{"x"}}}) {
		t.Error("HasConditions = true without conditions")
	}
}

func TestActive(t *testing.T) {
	ps := vmParams(t)
	tests := []struct {
		name   string
		input  in
		active map[string]bool // only the listed names are checked
		values in              // only the listed names are checked; nil = must be absent
	}{
		{
			name:  "defaults: everything conditional inactive, chains too",
			input: in{"name": "vm1"},
			active: map[string]bool{"kind_workers": false, "dev_features": false, "dev_kind_workers": false,
				"runner_toolchain": false, "runner_extra": false, "size": true},
			values: in{"use_case": "baseos", "kind_workers": nil},
		},
		{
			name:   "multiselect controller matches one of its values",
			input:  in{"use_case": "dev", "dev_features": []any{"go", "kind"}},
			active: map[string]bool{"dev_features": true, "dev_kind_workers": true},
		},
		{
			name:   "multiselect default [docker] has no kind",
			input:  in{"use_case": "dev"},
			active: map[string]bool{"dev_features": true, "dev_kind_workers": false},
		},
		{
			name:   "inactive controller has no value (chain)",
			input:  in{"use_case": "baseos", "dev_features": []any{"kind"}},
			active: map[string]bool{"dev_features": false, "dev_kind_workers": false},
		},
		{
			name:   "boolean default true matches \"true\"",
			input:  in{"use_case": "github-runner"},
			active: map[string]bool{"runner_toolchain": true, "runner_extra": true},
		},
		{
			name:   "boolean false",
			input:  in{"use_case": "github-runner", "runner_toolchain": false},
			active: map[string]bool{"runner_extra": false},
		},
		{
			name:   "boolean as string (form / -p)",
			input:  in{"use_case": "github-runner", "runner_toolchain": "false"},
			active: map[string]bool{"runner_extra": false},
		},
		{
			name:   "empty string counts as not set: default decides",
			input:  in{"use_case": ""},
			active: map[string]bool{"kind_workers": false},
			values: in{"use_case": "baseos"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			active, values := conditions.Active(ps, tt.input)
			for n, want := range tt.active {
				if active[n] != want {
					t.Errorf("active[%s] = %v, want %v", n, active[n], want)
				}
			}
			for n, want := range tt.values {
				got, ok := values[n]
				if want == nil {
					if ok {
						t.Errorf("values[%s] = %v, want absent", n, got)
					}
					continue
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("values[%s] = %v, want %v", n, got, want)
				}
			}
		})
	}
}

func TestAllowedValues(t *testing.T) {
	size := param(t, vmParams(t), "size")
	two := templates.Parameter{EnumWhen: map[string]map[string][]string{
		"a": {"x": {"1", "2", "3"}},
		"b": {"y": {"2", "3", "4"}},
	}}
	tests := []struct {
		name   string
		p      templates.Parameter
		values in
		want   []string
	}{
		{"narrowed", size, in{"use_case": "dev"}, []string{"large", "xlarge"}},
		{"unlisted controller value: enum", size, in{"use_case": "kind"}, []string{"small", "medium", "large", "xlarge"}},
		{"no controller value: enum", size, in{}, []string{"small", "medium", "large", "xlarge"}},
		{"no enum, no enumWhen: unrestricted", templates.Parameter{Name: "free"}, nil, nil},
		{"two controllers intersect", two, in{"a": "x", "b": "y"}, []string{"2", "3"}},
		{"no enum takes the list as is", two, in{"b": "y"}, []string{"2", "3", "4"}},
		{"no enum, unlisted: unrestricted", two, in{"a": "z"}, nil},
		{"values compare as strings", templates.Parameter{Enum: []string{"a", "b"},
			EnumWhen: map[string]map[string][]string{"flag": {"true": {"b"}}}}, in{"flag": true}, []string{"b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conditions.AllowedValues(tt.p, tt.values); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AllowedValues = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	ps := vmParams(t)

	// Values of inactive parameters are dropped, others kept as given.
	got, err := conditions.Check(ps, in{
		"name": "vm1", "use_case": "baseos", "size": "small",
		"kind_workers": "5", "dev_features": []any{"go"}, "runner_toolchain": false, "extra": "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (in{"name": "vm1", "use_case": "baseos", "size": "small", "extra": "x"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Check = %v, want %v", got, want)
	}

	got, err = conditions.Check(ps, in{"name": "vm1", "use_case": "kind", "kind_workers": "2"})
	if err != nil || got["kind_workers"] != "2" {
		t.Errorf("kind: got %v, %v", got, err)
	}

	tests := []struct {
		name  string
		input in
		want  conditions.FieldErrors // nil = valid
	}{
		{"enumWhen narrows", in{"name": "vm1", "use_case": "dev", "size": "small"},
			conditions.FieldErrors{"size": "use_case dev allows large or xlarge, got small"}},
		{"default of an enumWhen parameter is checked", in{"name": "vm1", "use_case": "github-runner"},
			conditions.FieldErrors{"size": "use_case github-runner allows large or xlarge, got medium (default)"}},
		{"three allowed", in{"name": "vm1", "use_case": "rke2", "size": "small"},
			conditions.FieldErrors{"size": "use_case rke2 allows medium, large or xlarge, got small"}},
		{"allowed", in{"name": "vm1", "use_case": "rke2", "size": "medium"}, nil},
		{"required, enum; inactive not checked", in{"use_case": "nope", "dev_features": []any{"evil"}},
			conditions.FieldErrors{"name": "is required", "use_case": "must be one of baseos, kind, dev, rke2, github-runner, got nope"}},
		{"pattern, multiselect items", in{"name": "Bad Name", "use_case": "dev", "size": "large", "dev_features": []any{"go", "evil"}},
			conditions.FieldErrors{"name": "does not match ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", "dev_features": "must be one of docker, go, kind, got evil"}},
		{"boolean", in{"name": "vm1", "use_case": "github-runner", "size": "large", "runner_toolchain": "yes"},
			conditions.FieldErrors{"runner_toolchain": "must be true or false"}},
		{"empty strings left to the default", in{"name": "vm1", "size": ""}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := conditions.Check(ps, tt.input)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if got := fieldsOf(t, err); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("fields = %v, want %v", got, tt.want)
			}
		})
	}

	_, err = conditions.Check(ps, in{"name": "vm1", "use_case": "dev", "size": "small"})
	if err == nil || !strings.HasPrefix(err.Error(), "invalid parameters: size:") {
		t.Errorf("Error() = %v", err)
	}
}

func TestCheckLength(t *testing.T) {
	two, five := 2, 5
	ps := []templates.Parameter{{Name: "s", Type: "string", MinLength: &two, MaxLength: &five}}
	for v, want := range map[string]string{"a": "needs at least 2 characters", "abcdef": "allows at most 5 characters", "abc": ""} {
		if got := conditions.CheckValue(ps[0], v, nil); got != want {
			t.Errorf("CheckValue(%q) = %q, want %q", v, got, want)
		}
	}
}

// A template without conditions keeps today's behaviour for valid input.
func TestCheckWithoutConditions(t *testing.T) {
	ps := []templates.Parameter{
		{Name: "a", Type: "string", Default: "x"},
		{Name: "b", Type: "string", Required: true, ValueFrom: &templates.ValueFromSpec{Function: "f"}},
	}
	input := in{"a": "y", "other": 1}
	got, err := conditions.Check(ps, input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, input) {
		t.Errorf("Check = %v, want %v", got, input)
	}
	if out, dropped := conditions.Drop(ps, input); !reflect.DeepEqual(out, input) || dropped != nil {
		t.Errorf("Drop = %v, %v", out, dropped)
	}
}

func TestDrop(t *testing.T) {
	ps := vmParams(t)
	input := in{"name": "x", "use_case": "dev", "size": "small", "kind_workers": "2", "dev_kind_workers": "4", "extra": "y"}
	out, dropped := conditions.Drop(ps, input)
	if want := (in{"name": "x", "use_case": "dev", "size": "small", "extra": "y"}); !reflect.DeepEqual(out, want) {
		t.Errorf("Drop = %v, want %v", out, want)
	}
	if want := []string{"kind_workers", "dev_kind_workers"}; !reflect.DeepEqual(dropped, want) {
		t.Errorf("dropped = %v, want %v", dropped, want)
	}
	if _, ok := input["kind_workers"]; !ok {
		t.Error("Drop modified its input")
	}
}

func TestShowWhenText(t *testing.T) {
	p := templates.Parameter{ShowWhen: map[string][]string{"use_case": {"dev", "rke2"}, "size": {"large"}}}
	if got, want := conditions.ShowWhenText(p), "size is large and use_case is dev or rke2"; got != want {
		t.Errorf("ShowWhenText = %q, want %q", got, want)
	}
	if got := conditions.Controllers(templates.Parameter{
		ShowWhen: map[string][]string{"b": {"x"}},
		EnumWhen: map[string]map[string][]string{"a": {"x": {"y"}}, "b": {}},
	}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Controllers = %v", got)
	}
}
