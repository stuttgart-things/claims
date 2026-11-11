// Package conditions evaluates the parameter conditions of a ClaimTemplate
// (showWhen, enumWhen; claim-machinery-api#135) exactly as the API does
// (claim-machinery-api internal/claimtemplate/conditions.go), so the CLI can
// hide what does not apply, offer only the allowed values and drop what the
// API would ignore anyway.
//
// Values are compared as strings (fmt.Sprint), so a boolean parameter
// matches "true" / "false".
//
//	showWhen: {use_case: [dev]}
//	    The parameter applies only when use_case is dev. Several keys must
//	    all match (AND); the listed values are alternatives (OR). A
//	    multiselect controlling parameter matches when one of its selected
//	    values is listed.
//
//	enumWhen: {use_case: {dev: [large, xlarge], rke2: [medium, large, xlarge]}}
//	    While use_case is dev, the parameter allows only large and xlarge.
//	    A value of use_case that is not listed leaves the enum as it is.
//	    Several controlling parameters narrow it further (intersection).
//
// A parameter whose showWhen does not match is inactive: it is hidden, its
// value is not sent, and it has no value for the conditions of other
// parameters. The value a condition looks at is the one given, else the
// parameter's default. Templates without conditions behave as before.
package conditions

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/stuttgart-things/claims/internal/templates"
)

// FieldErrors maps a parameter name to what is wrong with its value.
type FieldErrors map[string]string

// ValidationError is returned by Check when the parameters break the
// template's rules.
type ValidationError struct {
	Fields FieldErrors
}

func (e *ValidationError) Error() string {
	names := make([]string, 0, len(e.Fields))
	for n := range e.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + ": " + e.Fields[n]
	}
	return "invalid parameters: " + strings.Join(parts, "; ")
}

// HasConditions reports whether any parameter carries showWhen or enumWhen
// (older APIs never send them).
func HasConditions(params []templates.Parameter) bool {
	for _, p := range params {
		if len(p.ShowWhen) > 0 || len(p.EnumWhen) > 0 {
			return true
		}
	}
	return false
}

// valueStrings returns v as the strings conditions compare: one per item of
// a list, else one; nil has none.
func valueStrings(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return t
	case []any:
		out := make([]string, len(t))
		for i, it := range t {
			out[i] = fmt.Sprint(it)
		}
		return out
	default:
		return []string{fmt.Sprint(t)}
	}
}

// IsEmpty reports whether v counts as not set: nil, "" or an empty list.
func IsEmpty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case []string:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

// Shown reports whether p's showWhen matches values (the effective values
// of the active parameters, see Active).
func Shown(p templates.Parameter, values map[string]any) bool {
	for ctrl, want := range p.ShowWhen {
		have := valueStrings(values[ctrl])
		if !slices.ContainsFunc(have, func(s string) bool { return slices.Contains(want, s) }) {
			return false
		}
	}
	return true
}

// AllowedValues returns the values p allows given values: its enum narrowed
// by enumWhen. nil means no restriction (no enum and no matching enumWhen).
func AllowedValues(p templates.Parameter, values map[string]any) []string {
	allowed := p.Enum
	restricted := len(p.Enum) > 0
	for _, ctrl := range sortedKeys(p.EnumWhen) {
		byValue := p.EnumWhen[ctrl]
		for _, v := range valueStrings(values[ctrl]) {
			list, ok := byValue[v]
			if !ok {
				continue
			}
			if !restricted {
				allowed, restricted = list, true
				continue
			}
			var both []string
			for _, a := range allowed {
				if slices.Contains(list, a) {
					both = append(both, a)
				}
			}
			allowed = both
		}
	}
	if !restricted {
		return nil
	}
	if allowed == nil {
		allowed = []string{}
	}
	return allowed
}

// effective returns the value a condition sees for p: the one given, else
// its default.
func effective(p templates.Parameter, input map[string]any) (any, bool) {
	if v, ok := input[p.Name]; ok && !IsEmpty(v) {
		return v, true
	}
	if p.Default != nil {
		return p.Default, true
	}
	return nil, false
}

// Active evaluates the showWhen conditions of params against input (the
// values given; defaults fill the rest). It returns which parameters are
// active and the effective values of the active ones. An inactive parameter
// has no value, so a condition on it does not match; this is repeated
// until stable, so chains (A hides B, B shows C) resolve.
func Active(params []templates.Parameter, input map[string]any) (map[string]bool, map[string]any) {
	active := make(map[string]bool, len(params))
	for _, p := range params {
		active[p.Name] = true
	}
	values := map[string]any{}
	for range len(params) + 1 {
		values = make(map[string]any, len(params))
		for _, p := range params {
			if !active[p.Name] {
				continue
			}
			if v, ok := effective(p, input); ok {
				values[p.Name] = v
			}
		}
		changed := false
		for _, p := range params {
			if len(p.ShowWhen) == 0 {
				continue
			}
			if shown := Shown(p, values); shown != active[p.Name] {
				active[p.Name] = shown
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return active, values
}

// Drop returns a copy of input without the values of inactive parameters
// (the API ignores them) and the names it dropped, in template order.
// Keys that are not parameters of the template are kept.
func Drop(params []templates.Parameter, input map[string]any) (map[string]any, []string) {
	active, _ := Active(params, input)
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	var dropped []string
	for _, p := range params {
		if active[p.Name] {
			continue
		}
		if _, ok := out[p.Name]; ok {
			delete(out, p.Name)
			dropped = append(dropped, p.Name)
		}
	}
	return out, dropped
}

// Check validates input against params like the API does before
// rendering (claim-machinery-api CheckParams):
//   - a value for an inactive parameter is dropped from the result;
//   - a required active parameter needs a value, unless it has a default or
//     a valueFrom function;
//   - a value given must be one of the allowed values (enum narrowed by
//     enumWhen), match pattern and minLength/maxLength; a boolean parameter
//     takes true/false;
//   - the default of a parameter with enumWhen must be allowed too when no
//     value is given.
//
// Empty strings are left alone (the default or KCL decides). input is not
// modified. The error is a *ValidationError.
func Check(params []templates.Parameter, input map[string]any) (map[string]any, error) {
	active, values := Active(params, input)
	out := make(map[string]any, len(input))
	for k, v := range input {
		out[k] = v
	}
	fields := FieldErrors{}
	for _, p := range params {
		if !active[p.Name] {
			delete(out, p.Name)
			continue
		}
		v, sent := input[p.Name]
		if !sent || IsEmpty(v) {
			if p.Required && p.Default == nil && p.ValueFrom == nil {
				fields[p.Name] = "is required"
				continue
			}
			if len(p.EnumWhen) > 0 && p.Default != nil {
				if msg := checkAllowed(p, p.Default, values); msg != "" {
					fields[p.Name] = msg + " (default)"
				}
			}
			continue
		}
		if msg := CheckValue(p, v, values); msg != "" {
			fields[p.Name] = msg
		}
	}
	if len(fields) > 0 {
		return nil, &ValidationError{Fields: fields}
	}
	return out, nil
}

// CheckValue checks one value given for p against its allowed values (given
// the effective values of the others), type boolean, pattern and length.
// It returns what is wrong, or "".
func CheckValue(p templates.Parameter, v any, values map[string]any) string {
	if msg := checkAllowed(p, v, values); msg != "" {
		return msg
	}
	s, isString := v.(string)
	if p.Type == "boolean" {
		if _, isBool := v.(bool); !isBool && (!isString || (s != "true" && s != "false")) {
			return "must be true or false"
		}
		return ""
	}
	if !isString {
		return ""
	}
	n := utf8.RuneCountInString(s)
	if p.MinLength != nil && n < *p.MinLength {
		return fmt.Sprintf("needs at least %d characters", *p.MinLength)
	}
	if p.MaxLength != nil && n > *p.MaxLength {
		return fmt.Sprintf("allows at most %d characters", *p.MaxLength)
	}
	if p.Pattern != "" {
		if re, err := regexp.Compile(p.Pattern); err == nil && !re.MatchString(s) {
			return fmt.Sprintf("does not match %s", p.Pattern)
		}
	}
	return ""
}

// checkAllowed checks every value of v (one, or each of a list) against
// the parameter's allowed values.
func checkAllowed(p templates.Parameter, v any, values map[string]any) string {
	allowed := AllowedValues(p, values)
	if allowed == nil {
		return ""
	}
	for _, s := range valueStrings(v) {
		if slices.Contains(allowed, s) {
			continue
		}
		if why := enumWhenReason(p, values); why != "" {
			return fmt.Sprintf("%s allows %s, got %s", why, joinOr(allowed), s)
		}
		return fmt.Sprintf("must be one of %s, got %s", strings.Join(allowed, ", "), s)
	}
	return ""
}

// enumWhenReason names the controlling values that narrow the enum, e.g.
// "use_case dev", or "".
func enumWhenReason(p templates.Parameter, values map[string]any) string {
	var why []string
	for _, ctrl := range sortedKeys(p.EnumWhen) {
		for _, v := range valueStrings(values[ctrl]) {
			if _, ok := p.EnumWhen[ctrl][v]; ok {
				why = append(why, ctrl+" "+v)
			}
		}
	}
	return strings.Join(why, " and ")
}

// ShowWhenText describes p's showWhen for humans, e.g. "use_case is kind"
// or "use_case is dev or rke2 and size is large"; "" without showWhen.
func ShowWhenText(p templates.Parameter) string {
	var parts []string
	for _, ctrl := range sortedKeys(p.ShowWhen) {
		parts = append(parts, ctrl+" is "+strings.Join(p.ShowWhen[ctrl], " or "))
	}
	return strings.Join(parts, " and ")
}

// Controllers returns the parameters p's conditions look at (showWhen and
// enumWhen keys), sorted.
func Controllers(p templates.Parameter) []string {
	seen := map[string]bool{}
	for k := range p.ShowWhen {
		seen[k] = true
	}
	for k := range p.EnumWhen {
		seen[k] = true
	}
	return sortedKeys(seen)
}

func joinOr(list []string) string {
	switch len(list) {
	case 0:
		return "no value"
	case 1:
		return "only " + list[0]
	}
	return strings.Join(list[:len(list)-1], ", ") + " or " + list[len(list)-1]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
