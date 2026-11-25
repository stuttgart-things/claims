package sops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigFile is the name of the sops config at the repository root.
const ConfigFile = ".sops.yaml"

// Rule is one creation rule of .sops.yaml (the parts claims reads).
type Rule struct {
	Index          int // position in creation_rules
	PathRegex      string
	EncryptedRegex string
	Age            []string // recipients, from age: or key_groups[].age
	re             *regexp.Regexp
}

// Config is a parsed .sops.yaml.
type Config struct {
	Path  string
	Rules []Rule
}

type rawRule struct {
	PathRegex      string    `yaml:"path_regex"`
	EncryptedRegex string    `yaml:"encrypted_regex"`
	Age            yaml.Node `yaml:"age"`
	KeyGroups      []struct {
		Age yaml.Node `yaml:"age"`
	} `yaml:"key_groups"`
}

// LoadConfig reads <repoRoot>/.sops.yaml. claims does not walk up from the
// file like sops can: the rules for secrets/ belong at the repository root,
// where sops (run there) finds them too.
func LoadConfig(repoRoot string) (*Config, error) {
	p := filepath.Join(repoRoot, ConfigFile)
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s not found: the fleet repository needs a root %s with a creation rule per lab (see docs/13-secrets.md)", p, ConfigFile)
	}
	if err != nil {
		return nil, err
	}
	var raw struct {
		CreationRules []rawRule `yaml:"creation_rules"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", p, err)
	}
	c := &Config{Path: p}
	for i, r := range raw.CreationRules {
		rule := Rule{Index: i, PathRegex: r.PathRegex, EncryptedRegex: r.EncryptedRegex}
		rule.Age = append(rule.Age, ageList(r.Age)...)
		for _, g := range r.KeyGroups {
			rule.Age = append(rule.Age, ageList(g.Age)...)
		}
		if r.PathRegex != "" {
			re, err := regexp.Compile(r.PathRegex)
			if err != nil {
				return nil, fmt.Errorf("%s: creation_rules[%d].path_regex: %w", p, i, err)
			}
			rule.re = re
		}
		c.Rules = append(c.Rules, rule)
	}
	return c, nil
}

// ageList reads age: as a comma/newline separated string or a list.
func ageList(n yaml.Node) []string {
	var parts []string
	switch n.Kind {
	case yaml.ScalarNode:
		parts = strings.FieldsFunc(n.Value, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' })
	case yaml.SequenceNode:
		for _, c := range n.Content {
			parts = append(parts, strings.FieldsFunc(c.Value, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' })...)
		}
	}
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// RuleFor returns the rule sops applies to rel (slash-separated, relative to
// the repository root): the first one whose path_regex matches. A rule
// without path_regex matches everything, as in sops. No match is an error:
// claims never guesses recipients.
func (c *Config) RuleFor(rel string) (*Rule, error) {
	rel = filepath.ToSlash(rel)
	for i := range c.Rules {
		r := &c.Rules[i]
		if r.re == nil || r.re.MatchString(rel) {
			if len(r.Age) == 0 {
				return nil, fmt.Errorf("%s: creation_rules[%d] (path_regex %q) matches %s but names no age recipients", c.Path, r.Index, r.PathRegex, rel)
			}
			return r, nil
		}
	}
	return nil, fmt.Errorf("%s has no creation rule for %s: add one, e.g. path_regex: ^secrets/<lab>/.*\\.enc\\.yaml$ with the lab's and the recovery recipient (see docs/13-secrets.md)", c.Path, rel)
}

// labRulePattern finds the per-lab rules: a path_regex naming a directory
// below secrets/ (secrets/labul/...), not a catch-all like ^secrets/.*.
var labRulePattern = regexp.MustCompile(`secrets/[a-z0-9][a-z0-9-]*/`)

// SharedRecipients returns the recipients common to every per-lab rule
// (path_regex secrets/<lab>/...): with two or more lab rules, these are the
// offline recovery recipient(s). Catch-all rules for other files below
// secrets/ do not count. nil when there are fewer than two lab rules.
func (c *Config) SharedRecipients() []string {
	var rules []Rule
	for _, r := range c.Rules {
		if labRulePattern.MatchString(r.PathRegex) {
			rules = append(rules, r)
		}
	}
	if len(rules) < 2 {
		return nil
	}
	count := map[string]int{}
	for _, r := range rules {
		seen := map[string]bool{}
		for _, a := range r.Age {
			if !seen[a] {
				seen[a] = true
				count[a]++
			}
		}
	}
	var out []string
	for a, n := range count {
		if n == len(rules) {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// Has reports whether r encrypts for recipient.
func (r *Rule) Has(recipient string) bool {
	for _, a := range r.Age {
		if a == recipient {
			return true
		}
	}
	return false
}

// ShortRecipient abbreviates an age recipient for display: age1abcd…wxyz.
func ShortRecipient(r string) string {
	if len(r) <= 16 {
		return r
	}
	return r[:8] + "…" + r[len(r)-6:]
}
