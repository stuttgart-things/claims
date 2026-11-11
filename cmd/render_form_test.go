package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/stuttgart-things/claims/internal/templates"
)

// formTemplateJSON follows the xr-native-vm templates with conditions
// (claim-machinery-api#135, kcl#396).
const formTemplateJSON = `{
  "metadata": {"name": "native-vm", "title": "VM"},
  "spec": {"type": "xr-native-vm", "source": "oci://example/xr-native-vm", "parameters": [
    {"name": "templateName", "type": "string", "default": "labul-proxmox", "hidden": true},
    {"name": "name", "title": "VM Name", "type": "string", "required": true, "pattern": "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"},
    {"name": "use_case", "type": "string", "default": "baseos", "enum": ["baseos", "kind", "dev", "rke2", "github-runner"]},
    {"name": "size", "type": "string", "default": "medium", "enum": ["small", "medium", "large", "xlarge"], "allowRandom": true,
     "enumWhen": {"use_case": {"dev": ["large", "xlarge"], "github-runner": ["large", "xlarge"], "rke2": ["medium", "large", "xlarge"]}}},
    {"name": "kind_workers", "type": "string", "default": "1", "showWhen": {"use_case": ["kind"]}},
    {"name": "dev_features", "type": "array", "multiselect": true, "enum": ["docker", "go", "kind"], "default": ["docker"],
     "showWhen": {"use_case": ["dev"]}},
    {"name": "dev_go_version", "type": "string", "default": "1.27.4", "showWhen": {"use_case": ["dev"]}},
    {"name": "dev_kind_workers", "type": "string", "default": "3", "showWhen": {"dev_features": ["kind"]}},
    {"name": "runner_toolchain", "type": "boolean", "default": true, "showWhen": {"use_case": ["github-runner"]}},
    {"name": "owner", "type": "string", "default": ""}
  ]}
}`

func formTemplate(t *testing.T) *templates.ClaimTemplate {
	t.Helper()
	var tmpl templates.ClaimTemplate
	if err := json.Unmarshal([]byte(formTemplateJSON), &tmpl); err != nil {
		t.Fatal(err)
	}
	return &tmpl
}

func groupNames(groups []formGroup) [][]string {
	var out [][]string
	for _, g := range groups {
		var names []string
		for _, p := range g.params {
			names = append(names, p.Name)
		}
		out = append(out, names)
	}
	return out
}

func TestLayoutGroups(t *testing.T) {
	got := groupNames(layoutGroups(formTemplate(t).Spec.Parameters))
	want := [][]string{
		{"name", "use_case", "size"},       // hidden templateName has no field
		{"kind_workers"},                   // use_case is kind
		{"dev_features", "dev_go_version"}, // same showWhen: one page
		{"dev_kind_workers"},               // dev_features is kind
		{"runner_toolchain"},               // use_case is github-runner
		{"owner"},                          // unconditional again
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layoutGroups =\n%v\nwant\n%v", got, want)
	}

	// Without conditions: pages of maxFieldsPerGroup, as before.
	var flat []templates.Parameter
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		flat = append(flat, templates.Parameter{Name: n, Type: "string"})
	}
	if got := groupNames(layoutGroups(flat)); !reflect.DeepEqual(got, [][]string{{"a", "b", "c", "d", "e"}, {"f", "g"}}) {
		t.Errorf("layoutGroups without conditions = %v", got)
	}
}

func TestParamFormResult(t *testing.T) {
	tmpl := formTemplate(t)
	tests := []struct {
		name string
		set  map[string]string
		ms   map[string][]string
		want map[string]any
	}{
		{
			name: "baseos: conditional values are not sent",
			set:  map[string]string{"name": "vm1", "kind_workers": "5", "dev_go_version": "1.0"},
			want: map[string]any{"templateName": "labul-proxmox", "name": "vm1", "use_case": "baseos", "size": "medium"},
		},
		{
			name: "kind",
			set:  map[string]string{"name": "vm1", "use_case": "kind", "kind_workers": "2"},
			want: map[string]any{"templateName": "labul-proxmox", "name": "vm1", "use_case": "kind", "size": "medium", "kind_workers": "2"},
		},
		{
			name: "dev with kind feature: chain shows dev_kind_workers",
			set:  map[string]string{"name": "vm1", "use_case": "dev", "size": "large"},
			ms:   map[string][]string{"dev_features": {"go", "kind"}},
			want: map[string]any{"templateName": "labul-proxmox", "name": "vm1", "use_case": "dev", "size": "large",
				"dev_features": []any{"go", "kind"}, "dev_go_version": "1.27.4", "dev_kind_workers": "3"},
		},
		{
			name: "dev without kind feature",
			set:  map[string]string{"name": "vm1", "use_case": "dev", "size": "xlarge"},
			want: map[string]any{"templateName": "labul-proxmox", "name": "vm1", "use_case": "dev", "size": "xlarge",
				"dev_features": []any{"docker"}, "dev_go_version": "1.27.4"},
		},
		{
			name: "github-runner: boolean",
			set:  map[string]string{"name": "vm1", "use_case": "github-runner", "size": "large", "runner_toolchain": "false"},
			want: map[string]any{"templateName": "labul-proxmox", "name": "vm1", "use_case": "github-runner", "size": "large", "runner_toolchain": "false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newParamForm(tmpl, nil)
			for k, v := range tt.set {
				*f.values[k] = v
			}
			for k, v := range tt.ms {
				*f.multi[k] = v
			}
			if got := f.result(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("result =\n%v\nwant\n%v", got, tt.want)
			}
		})
	}
}

func TestParamFormRandomWithinAllowed(t *testing.T) {
	f := newParamForm(formTemplate(t), nil)
	*f.values["name"] = "vm1"
	*f.values["use_case"] = "dev"
	for range 20 {
		*f.values["size"] = randomMarker
		if got := f.result()["size"]; got != "large" && got != "xlarge" {
			t.Fatalf("random size for dev = %v", got)
		}
	}
}

func TestParamFormPrefill(t *testing.T) {
	f := newParamForm(formTemplate(t), map[string]any{"name": "vm1", "use_case": "dev", "dev_features": []any{"go"}})
	if *f.values["name"] != "vm1" || *f.values["use_case"] != "dev" || *f.values["size"] != "medium" {
		t.Errorf("values = %s %s %s", *f.values["name"], *f.values["use_case"], *f.values["size"])
	}
	if !reflect.DeepEqual(*f.multi["dev_features"], []string{"go"}) {
		t.Errorf("dev_features = %v", *f.multi["dev_features"])
	}
}

func TestParamFormValidate(t *testing.T) {
	tmpl := formTemplate(t)
	param := func(name string) templates.Parameter {
		for _, p := range tmpl.Spec.Parameters {
			if p.Name == name {
				return p
			}
		}
		t.Fatalf("no %s", name)
		return templates.Parameter{}
	}
	f := newParamForm(tmpl, nil)
	*f.values["use_case"] = "dev"

	tests := []struct {
		param string
		value string
		want  string // "" = valid
	}{
		{"name", "", "is required"},
		{"name", "Bad Name", "does not match ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"},
		{"name", "vm1", ""},
		{"size", "small", "use_case dev allows large or xlarge, got small"},
		{"size", "large", ""},
		{"size", randomMarker, ""},
		{"use_case", "nope", "must be one of baseos, kind, dev, rke2, github-runner, got nope"},
		{"owner", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.param+"="+tt.value, func(t *testing.T) {
			err := f.validate(param(tt.param), tt.value)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tt.want {
				t.Errorf("validate = %q, want %q", got, tt.want)
			}
		})
	}

	if err := f.validateAny(param("dev_features"), []any{"go", "evil"}); err == nil || !strings.Contains(err.Error(), "got evil") {
		t.Errorf("multiselect validate = %v", err)
	}
	integer := templates.Parameter{Name: "n", Type: "integer"}
	if err := f.validate(integer, "x"); err == nil || err.Error() != "must be a number" {
		t.Errorf("integer validate = %v", err)
	}
}

func TestParamFormFieldErrors(t *testing.T) {
	tmpl := formTemplate(t)
	f := newParamForm(tmpl, nil)
	*f.values["name"] = "vm1"
	*f.values["use_case"] = "github-runner" // size stays at its default medium
	got := f.fieldErrors(f.result())
	if want := map[string]string{"size": "use_case github-runner allows large or xlarge, got medium"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fieldErrors = %v, want %v", got, want)
	}

	*f.values["size"] = "large"
	if got := f.fieldErrors(f.result()); got != nil {
		t.Errorf("fieldErrors = %v, want nil", got)
	}

	// A failing parameter without a field cannot be fixed in the form: the
	// API decides.
	*f.values["templateName"] = "has spaces"
	tmpl.Spec.Parameters[0].Pattern = "^[a-z-]+$"
	if got := f.fieldErrors(f.result()); got != nil {
		t.Errorf("fieldErrors with a hidden parameter = %v, want nil", got)
	}
}

func TestFormatFieldErrors(t *testing.T) {
	got := formatFieldErrors(formTemplate(t).Spec.Parameters, map[string]string{
		"zzz": "unknown", "size": "bad size", "name": "is required",
	})
	want := "  name: is required\n  size: bad size\n  zzz: unknown"
	if got != want {
		t.Errorf("formatFieldErrors =\n%s\nwant\n%s", got, want)
	}
}

func TestAPIFieldErrors(t *testing.T) {
	err := &templates.APIError{Status: 400, Message: "invalid parameters", Fields: map[string]string{"size": "x"}}
	if got := apiFieldErrors(err); !reflect.DeepEqual(got, map[string]string{"size": "x"}) {
		t.Errorf("apiFieldErrors = %v", got)
	}
	if apiFieldErrors(errors.New("boom")) != nil || apiFieldErrors(&templates.APIError{Status: 400, Message: "assert"}) != nil {
		t.Error("apiFieldErrors without fields should be nil")
	}
}

// pump feeds msg to g and then the messages its commands produce, a few
// rounds, the way the bubbletea loop would (commands that do not answer
// quickly, like spinner ticks, are dropped).
func pump(t *testing.T, g *huh.Group, msg tea.Msg) {
	t.Helper()
	msgs := []tea.Msg{msg}
	for round := 0; round < 4 && len(msgs) > 0; round++ {
		var cmds []tea.Cmd
		for _, m := range msgs {
			_, cmd := g.Update(m)
			cmds = append(cmds, cmd)
		}
		msgs = nil
		results := make(chan tea.Msg, 64)
		pending := 0
		run := func(cmd tea.Cmd) {
			if cmd == nil {
				return
			}
			pending++
			go func() { results <- cmd() }()
		}
		for _, c := range cmds {
			run(c)
		}
		deadline := time.After(50 * time.Millisecond)
	collect:
		for pending > 0 {
			select {
			case m := <-results:
				pending--
				if batch, ok := m.(tea.BatchMsg); ok {
					for _, c := range batch {
						run(c)
					}
					continue
				}
				if m != nil {
					msgs = append(msgs, m)
				}
			case <-deadline:
				break collect
			}
		}
	}
}

// The size select offers only what use_case allows and moves to the first
// allowed value when its value drops out.
func TestSizeOptionsFollowUseCase(t *testing.T) {
	tmpl := formTemplate(t)
	f := newParamForm(tmpl, nil)
	var size templates.Parameter
	for _, p := range tmpl.Spec.Parameters {
		if p.Name == "size" {
			size = p
		}
	}
	field := f.field(size, "")
	field.WithKeyMap(huh.NewDefaultKeyMap()) // as the form does
	g := huh.NewGroup(field)
	g.Init()

	steps := []struct {
		useCase, before, want string
		shown, notShown       []string
	}{
		{"baseos", "medium", "medium", []string{"small", "medium", "large", "xlarge"}, nil},
		{"dev", "medium", "large", []string{"large", "xlarge"}, []string{"small", "medium"}},   // first allowed
		{"rke2", "xlarge", "xlarge", []string{"medium", "large", "xlarge"}, []string{"small"}}, // still allowed: kept
		{"dev", "xlarge", "xlarge", []string{"large", "xlarge"}, []string{"medium"}},           // seen before: no stale cursor
		{"github-runner", "medium", "large", []string{"large", "xlarge"}, []string{"medium"}},
		{"kind", "large", "large", []string{"small", "medium", "large", "xlarge"}, nil}, // unlisted use case: full enum, list from the top
	}
	for _, s := range steps {
		*f.values["use_case"] = s.useCase
		*f.values["size"] = s.before
		pump(t, g, struct{}{})
		if got := *f.values["size"]; got != s.want {
			t.Errorf("use_case %s, size %s: got %s, want %s", s.useCase, s.before, got, s.want)
		}
		view := g.Content()
		if !strings.Contains(view, "> "+s.want) {
			t.Errorf("use_case %s: cursor not on %s:\n%s", s.useCase, s.want, view)
		}
		for _, o := range s.shown {
			if !strings.Contains(view, o) {
				t.Errorf("use_case %s: option %s not shown:\n%s", s.useCase, o, view)
			}
		}
		for _, o := range s.notShown {
			if strings.Contains(view, " "+o+" ") || strings.HasSuffix(strings.TrimRight(view, " \n"), " "+o) {
				t.Errorf("use_case %s: option %s shown:\n%s", s.useCase, o, view)
			}
		}
	}
}

func TestFitToTerminal(t *testing.T) {
	orig := terminalSize
	t.Cleanup(func() { terminalSize = orig })
	terminalSize = func() (int, int, bool) { return 60, 20, true }

	// A page taller than the terminal: 5 fields, one a 13-option
	// multi-select (like the VM templates' dev_features).
	var enum []string
	for i := range 13 {
		enum = append(enum, strings.Repeat("x", i+1))
	}
	tmpl := &templates.ClaimTemplate{Spec: templates.ClaimTemplateSpec{Parameters: []templates.Parameter{
		{Name: "a", Title: "A", Description: strings.Repeat("long description ", 10), Type: "string"},
		{Name: "b", Title: "B", Type: "string", Enum: []string{"1", "2", "3", "4"}},
		{Name: "c", Title: "C", Type: "array", Multiselect: true, Enum: enum},
		{Name: "d", Title: "D", Type: "string"},
		{Name: "e", Title: "E", Type: "boolean"},
	}}}
	_, groups := newParamForm(tmpl, nil).build(nil)
	if h := lipgloss.Height(groups[0].Content()); h <= 20 {
		t.Fatalf("test page is only %d lines high", h)
	}
	fitToTerminal(groups)
	view := groups[0].View()
	if h := lipgloss.Height(view); h > 19 {
		t.Errorf("first frame is %d lines high on a 20-line terminal", h)
	}
	if w := lipgloss.Width(view); w > 60 {
		t.Errorf("first frame is %d columns wide on a 60-column terminal", w)
	}

	// No terminal: left alone.
	terminalSize = func() (int, int, bool) { return 0, 0, false }
	_, groups = newParamForm(tmpl, nil).build(nil)
	before := groups[0].View()
	fitToTerminal(groups)
	if groups[0].View() != before {
		t.Error("fitToTerminal changed the form without a terminal")
	}
}

// TestRenderStdoutConditions: non-interactive render does not send the
// values of inactive parameters and prints the API's field errors one per
// line.
func TestRenderStdoutConditions(t *testing.T) {
	var sent []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/order") {
			var req templates.OrderRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			sent = append(sent, req.Parameters)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid parameters","fields":{"size":"use_case dev allows large or xlarge, got small"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[` + formTemplateJSON + `]}`))
	}))
	t.Cleanup(srv.Close)

	config := &RenderConfig{
		APIUrl:          srv.URL,
		Templates:       []string{"native-vm"},
		InlineParamsRaw: []string{"name=x", "use_case=dev", "size=small", "kind_workers=4", "runner_toolchain=false"},
		Stdout:          true,
	}
	var err error
	_, stderr := captureStd(t, func() { err = runNonInteractive(config) })
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d orders", len(sent))
	}
	if want := (map[string]any{"name": "x", "use_case": "dev", "size": "small"}); !reflect.DeepEqual(sent[0], want) {
		t.Errorf("sent %v, want %v", sent[0], want)
	}
	for _, want := range []string{
		"Not sending kind_workers: only applies when use_case is kind",
		"Not sending runner_toolchain: only applies when use_case is github-runner",
		"ERROR: API returned 400: invalid parameters\n      size: use_case dev allows large or xlarge, got small\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr misses %q:\n%s", want, stderr)
		}
	}
}
