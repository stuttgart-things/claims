package cmd

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/stuttgart-things/claims/internal/conditions"
	"github.com/stuttgart-things/claims/internal/templates"
)

// The parameter form of one template (claims#115).
//
// Fields follow the order of spec.parameters (the template's decision
// order). A parameter with showWhen gets a group of its own (consecutive
// ones with the same showWhen share one), hidden while its condition does
// not match the answers so far; its value is not sent. A select whose
// parameter has enumWhen offers only the values the earlier answers allow
// and keeps its value if it is still allowed, else takes the first allowed
// one. Every field is validated (required, enum, pattern, length) before
// the form can be submitted; whatever still fails after the form (or comes
// back as a 400 from the API) is shown next to the field on the next round.
// Templates without conditions get the form they always had.

// maxFieldsPerGroup is how many unconditional fields share one page.
const maxFieldsPerGroup = 5

// paramForm holds the live values of one template's form. The huh fields
// write into values / multi; the conditions read them.
type paramForm struct {
	params []templates.Parameter
	values map[string]*string
	multi  map[string]*[]string
}

func isMultiselectParam(p templates.Parameter) bool {
	return p.Multiselect && len(p.Enum) > 0
}

// isFormField reports whether p gets a field: hidden and valueFrom
// parameters (resolved server-side) do not.
func isFormField(p templates.Parameter) bool {
	return !p.Hidden && p.ValueFrom == nil
}

// newParamForm starts every parameter at prev's value (an edit), else its
// default.
func newParamForm(tmpl *templates.ClaimTemplate, prev map[string]any) *paramForm {
	f := &paramForm{
		params: tmpl.Spec.Parameters,
		values: map[string]*string{},
		multi:  map[string]*[]string{},
	}
	for _, p := range f.params {
		v := p.Default
		if pv, ok := prev[p.Name]; ok {
			v = pv
		}
		if isMultiselectParam(p) {
			s := parseDefaultSlice(v)
			f.multi[p.Name] = &s
			continue
		}
		s := ""
		if v != nil {
			s = fmt.Sprintf("%v", v)
		}
		f.values[p.Name] = &s
	}
	return f
}

// input returns the values as they would be sent: non-empty strings and
// non-empty selections ([]any for JSON), the random marker unresolved.
func (f *paramForm) input() map[string]any {
	in := make(map[string]any, len(f.params))
	for _, p := range f.params {
		if m, ok := f.multi[p.Name]; ok {
			if len(*m) > 0 {
				vals := make([]any, len(*m))
				for i, v := range *m {
					vals[i] = v
				}
				in[p.Name] = vals
			}
			continue
		}
		if s := *f.values[p.Name]; s != "" {
			in[p.Name] = s
		}
	}
	return in
}

// active evaluates the showWhen conditions against the current answers.
func (f *paramForm) active() (map[string]bool, map[string]any) {
	return conditions.Active(f.params, f.input())
}

// result returns the parameters to send: only active ones, a random choice
// resolved among the allowed values.
func (f *paramForm) result() map[string]any {
	in := f.input()
	active, values := conditions.Active(f.params, in)
	for _, p := range f.params {
		if !active[p.Name] {
			delete(in, p.Name)
			continue
		}
		if in[p.Name] != randomMarker {
			continue
		}
		allowed := conditions.AllowedValues(p, values)
		if allowed == nil {
			allowed = p.Enum
		}
		if len(allowed) == 0 {
			delete(in, p.Name)
			continue
		}
		v := allowed[rand.Intn(len(allowed))]
		fmt.Printf("Random selection for %s: %s\n", p.Name, v)
		in[p.Name] = v
	}
	return in
}

// formGroup is one page of the form: the parameters on it and whether it
// is shown only under a showWhen.
type formGroup struct {
	params []templates.Parameter
	key    string // showWhenKey of its parameters ("" = always shown)
}

// showWhenKey identifies a showWhen so parameters with the same condition
// can share a group.
func showWhenKey(p templates.Parameter) string {
	if len(p.ShowWhen) == 0 {
		return ""
	}
	return conditions.ShowWhenText(p)
}

// layoutGroups splits the form fields into pages, in template order: a
// new page for every change of showWhen, and after maxFieldsPerGroup.
func layoutGroups(params []templates.Parameter) []formGroup {
	var groups []formGroup
	for _, p := range params {
		if !isFormField(p) {
			continue
		}
		key := showWhenKey(p)
		n := len(groups)
		if n == 0 || groups[n-1].key != key || len(groups[n-1].params) >= maxFieldsPerGroup {
			groups = append(groups, formGroup{key: key})
			n++
		}
		groups[n-1].params = append(groups[n-1].params, p)
	}
	return groups
}

// build returns the huh form and its groups; fieldErrs are shown next to
// their fields.
func (f *paramForm) build(fieldErrs map[string]string) (*huh.Form, []*huh.Group) {
	var groups []*huh.Group
	for _, g := range layoutGroups(f.params) {
		fields := make([]huh.Field, 0, len(g.params))
		for _, p := range g.params {
			fields = append(fields, f.field(p, fieldErrs[p.Name]))
		}
		group := huh.NewGroup(fields...)
		if g.key != "" {
			first := g.params[0].Name
			group = group.WithHideFunc(func() bool {
				active, _ := f.active()
				return !active[first]
			})
		}
		groups = append(groups, group)
	}
	return huh.NewForm(groups...), groups
}

// field creates the huh field for p.
func (f *paramForm) field(p templates.Parameter, fieldErr string) huh.Field {
	title := p.Title
	if title == "" {
		title = p.Name
	}
	if p.Required {
		title += " *"
	}
	description := p.Description
	if p.Pattern != "" {
		description += fmt.Sprintf(" (pattern: %s)", p.Pattern)
	}
	description = strings.TrimSpace(description)
	if fieldErr != "" {
		description = strings.TrimSpace("✗ " + fieldErr + "\n" + description)
	}

	if m, ok := f.multi[p.Name]; ok {
		return f.multiselectField(p, title, description, m)
	}
	value := f.values[p.Name]

	if len(p.Enum) > 0 {
		return f.selectField(p, title, description, value)
	}

	switch p.Type {
	case "boolean":
		return huh.NewSelect[string]().
			Title(title).
			Description(description).
			Options(huh.NewOption("true", "true"), huh.NewOption("false", "false")).
			Value(value)
	default: // string, integer
		return huh.NewInput().
			Title(title).
			Description(description).
			Placeholder(fmt.Sprintf("default: %v", p.Default)).
			Value(value).
			Validate(func(s string) error { return f.validate(p, s) })
	}
}

// selectField is a select over p's enum. With enumWhen the options are
// computed from the other answers: the current value if it is still
// allowed, else the first allowed one.
func (f *paramForm) selectField(p templates.Parameter, title, description string, value *string) huh.Field {
	options := func() (string, []huh.Option[string]) {
		allowed := p.Enum
		key := ""
		if len(p.EnumWhen) > 0 {
			_, values := f.active()
			if a := conditions.AllowedValues(p, values); a != nil {
				allowed = a
			}
			key = strings.Join(allowed, "\x00")
		}
		keep := *value == randomMarker && p.AllowRandom || slices.Contains(allowed, *value)
		if !keep {
			key += "\x00!" + *value // a value to replace: refresh
		}
		var opts []huh.Option[string]
		if p.AllowRandom {
			opts = append(opts, huh.NewOption(randomMarker, randomMarker))
		}
		for i, a := range allowed {
			opts = append(opts, huh.NewOption(a, a).Selected(!keep && i == 0))
		}
		return key, opts
	}
	key, opts := options()
	// Value last: Options (and OptionsFunc) scroll the list to the selected
	// option, which would hide the ones above it (e.g. small above medium).
	field := huh.NewSelect[string]().
		Title(title).
		Description(description).
		Options(opts...).
		Validate(func(s string) error { return f.validate(p, s) })
	if len(p.EnumWhen) == 0 {
		return field.Value(value)
	}
	return newEnumSelect(field, value, key, opts, options)
}

// enumSelect is a select whose options follow enumWhen.
//
// huh's OptionsFunc caches options by the hash of its bindings, and a cache
// hit neither re-selects the value nor updates it (the cursor can end up on
// another option than the value), and every load scrolls the list so the
// selected option is the first one shown. So: the options are computed on
// the event loop when the allowed values change and handed to OptionsFunc
// under a new generation (never a cache hit), and after a load the list is
// scrolled back to its top.
type enumSelect struct {
	*huh.Select[string]
	value   *string
	compute func() (string, []huh.Option[string])

	gen  int // the OptionsFunc binding
	key  string
	mu   sync.Mutex
	opts []huh.Option[string]
}

func newEnumSelect(s *huh.Select[string], value *string, key string, opts []huh.Option[string], compute func() (string, []huh.Option[string])) *enumSelect {
	e := &enumSelect{value: value, compute: compute, key: key, opts: opts}
	s = s.OptionsFunc(func() []huh.Option[string] {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.opts
	}, &e.gen)
	// Height 0: the list is as long as its options (OptionsFunc set a fixed
	// one); Value last, see selectField.
	e.Select = s.Height(0).Value(value)
	return e
}

// Update refreshes the options when the allowed values changed, then lets
// the select handle msg.
func (e *enumSelect) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); !ok {
		if key, opts := e.compute(); key != e.key {
			e.mu.Lock()
			e.key, e.opts = key, opts
			e.mu.Unlock()
			e.gen++
		}
	}
	m, cmd := e.Select.Update(msg)
	e.Select = m.(*huh.Select[string])
	if strings.Contains(fmt.Sprintf("%T", msg), "updateOptionsMsg") {
		e.scrollToTop()
	}
	return e, cmd
}

// scrollToTop undoes the scroll of an options load: up to the first option
// (which scrolls the list to its top) and back down to the value (which
// does not scroll while it fits).
func (e *enumSelect) scrollToTop() {
	e.mu.Lock()
	idx := slices.IndexFunc(e.opts, func(o huh.Option[string]) bool { return o.Value == *e.value })
	e.mu.Unlock()
	for _, k := range []tea.KeyType{tea.KeyUp, tea.KeyDown} {
		for range idx {
			m, _ := e.Select.Update(tea.KeyMsg{Type: k})
			e.Select = m.(*huh.Select[string])
		}
	}
}

// multiselectField is a multi-select over p's enum, narrowed by enumWhen
// (only the allowed values are offered; a selected value that is no longer
// allowed is dropped).
func (f *paramForm) multiselectField(p templates.Parameter, title, description string, value *[]string) huh.Field {
	field := huh.NewMultiSelect[string]().
		Title(title).
		Description(description).
		Options(huh.NewOptions(p.Enum...)...).
		Value(value).
		Validate(func(sel []string) error {
			vals := make([]any, len(sel))
			for i, s := range sel {
				vals[i] = s
			}
			return f.validateAny(p, vals)
		})
	if len(p.EnumWhen) == 0 {
		return field
	}
	e := &enumMultiSelect{compute: func() (string, []huh.Option[string]) {
		_, values := f.active()
		allowed := conditions.AllowedValues(p, values)
		if allowed == nil {
			allowed = p.Enum
		}
		opts := huh.NewOptions(allowed...)
		for i := range opts {
			opts[i] = opts[i].Selected(slices.Contains(*value, opts[i].Value))
		}
		return strings.Join(allowed, "\x00"), opts
	}}
	e.key, e.opts = e.compute()
	e.MultiSelect = field.OptionsFunc(func() []huh.Option[string] {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.opts
	}, &e.gen)
	return e
}

// enumMultiSelect is the multi-select counterpart of enumSelect (options
// computed on the event loop, a new generation per change).
type enumMultiSelect struct {
	*huh.MultiSelect[string]
	compute func() (string, []huh.Option[string])

	gen  int
	key  string
	mu   sync.Mutex
	opts []huh.Option[string]
}

func (e *enumMultiSelect) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyMsg); !ok {
		if key, opts := e.compute(); key != e.key {
			e.mu.Lock()
			e.key, e.opts = key, opts
			e.mu.Unlock()
			e.gen++
		}
	}
	m, cmd := e.MultiSelect.Update(msg)
	e.MultiSelect = m.(*huh.MultiSelect[string])
	return e, cmd
}

// validate checks a string answer for p (the field validators).
func (f *paramForm) validate(p templates.Parameter, s string) error {
	if s == randomMarker && p.AllowRandom {
		return nil
	}
	if s == "" {
		if p.Required && p.Default == nil && p.ValueFrom == nil {
			return errors.New("is required")
		}
		return nil
	}
	if p.Type == "integer" {
		if _, err := strconv.Atoi(s); err != nil {
			return errors.New("must be a number")
		}
	}
	return f.validateAny(p, s)
}

// validateAny checks v against p's allowed values (given the other
// answers), pattern and length.
func (f *paramForm) validateAny(p templates.Parameter, v any) error {
	if conditions.IsEmpty(v) {
		if p.Required && p.Default == nil && p.ValueFrom == nil {
			return errors.New("is required")
		}
		return nil
	}
	_, values := f.active()
	if msg := conditions.CheckValue(p, v, values); msg != "" {
		return errors.New(msg)
	}
	return nil
}

// fieldErrors returns what still fails for the shown fields after the form
// (e.g. an enumWhen default whose options were never refreshed); nil if
// nothing does, or if a failing parameter has no field to fix it in (the
// API has the last word then).
func (f *paramForm) fieldErrors(params map[string]any) map[string]string {
	_, err := conditions.Check(f.params, params)
	var verr *conditions.ValidationError
	if !errors.As(err, &verr) {
		return nil
	}
	for name := range verr.Fields {
		for _, p := range f.params {
			if p.Name == name && !isFormField(p) {
				return nil
			}
		}
	}
	return verr.Fields
}

// runParamForm shows the form until its answers pass the template's rules
// and returns the parameters to send. fieldErrs (e.g. from an API 400) are
// shown next to their fields in the first round.
func (f *paramForm) run(fieldErrs map[string]string) (map[string]any, error) {
	for {
		form, groups := f.build(fieldErrs)
		if len(groups) == 0 {
			return f.result(), nil
		}
		fitToTerminal(groups)
		if err := form.Run(); err != nil {
			return nil, err
		}
		params := f.result()
		fieldErrs = f.fieldErrors(params)
		if fieldErrs == nil {
			return params, nil
		}
		fmt.Println(styleLines(errorStyle, "Please fix:\n"+formatFieldErrors(f.params, fieldErrs)))
	}
}

// formatFieldErrors lists field errors one per line, in template order
// (unknown names last).
func formatFieldErrors(params []templates.Parameter, fields map[string]string) string {
	var lines []string
	seen := map[string]bool{}
	for _, p := range params {
		if msg, ok := fields[p.Name]; ok {
			lines = append(lines, fmt.Sprintf("  %s: %s", p.Name, msg))
			seen[p.Name] = true
		}
	}
	var rest []string
	for name := range fields {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	for _, name := range rest {
		lines = append(lines, fmt.Sprintf("  %s: %s", name, fields[name]))
	}
	return strings.Join(lines, "\n")
}

// apiFieldErrors returns the field errors of an API 400, if err is one.
func apiFieldErrors(err error) map[string]string {
	var apiErr *templates.APIError
	if errors.As(err, &apiErr) && len(apiErr.Fields) > 0 {
		return apiErr.Fields
	}
	return nil
}

// terminalSize returns the size of the terminal the forms draw on (huh
// writes to stderr); a var for tests.
var terminalSize = func() (width, height int, ok bool) {
	w, h, err := term.GetSize(os.Stderr.Fd())
	return w, h, err == nil && w > 0 && h > 0
}

// fitToTerminal sizes the groups to the terminal before the form starts.
//
// huh lays groups out at 80 columns and at their full height until the
// first WindowSizeMsg arrives, but bubbletea draws the first frame before
// that. A first frame taller (or wider) than the terminal scrolls, and
// the renderer can only move the cursor back over the visible lines: the
// top of that frame stays on screen as a static copy above the live form
// (claims#115; the VM templates' 5-field pages with a 13-option
// multi-select are taller than most terminals). Sized up front, the first
// frame fits; huh still adjusts the heights on later size messages.
func fitToTerminal(groups []*huh.Group) {
	w, h, ok := terminalSize()
	if !ok {
		return
	}
	for _, g := range groups {
		g.WithWidth(w)
		// Content plus the footer (a blank line and the help line); huh's
		// own footer arithmetic is a line short, so keep two lines free.
		need := lipgloss.Height(g.Content()) + 2
		g.WithHeight(min(need, h-2))
	}
}
