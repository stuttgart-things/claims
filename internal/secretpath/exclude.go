package secretpath

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// DenylistFile is the per-lab list of derived Vault paths, relative to the
// repository root: secrets/<lab>/.derived. Derived secrets (PKI, AppRole
// secret_ids, tokens, anything Vault issues itself) never go into git, so
// sync vault->sops skips them and diff does not report them as drift.
func DenylistFile(lab string) string {
	return filepath.Join(Root, lab, ".derived")
}

// Excluder matches Vault paths (<mount>/<path>) against glob patterns:
// '*' is any run of characters within a segment, '**' any run across
// segments, '?' one character. A pattern without wildcards also matches
// everything below it (kv/kubeconfigs matches kv/kubeconfigs/a).
type Excluder struct {
	patterns []string
	res      []*regexp.Regexp
}

// NewExcluder compiles patterns.
func NewExcluder(patterns ...string) (*Excluder, error) {
	e := &Excluder{}
	for _, p := range patterns {
		if err := e.Add(p); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// Add compiles one more pattern; blank patterns are ignored.
func (e *Excluder) Add(pattern string) error {
	p := strings.Trim(strings.TrimSpace(pattern), "/")
	if p == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("(/.*)?$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return fmt.Errorf("exclude pattern %q: %w", pattern, err)
	}
	e.patterns = append(e.patterns, p)
	e.res = append(e.res, re)
	return nil
}

// Match returns the first pattern that matches vaultPath, if any.
func (e *Excluder) Match(vaultPath string) (string, bool) {
	if e == nil {
		return "", false
	}
	vp := strings.Trim(vaultPath, "/")
	for i, re := range e.res {
		if re.MatchString(vp) {
			return e.patterns[i], true
		}
	}
	return "", false
}

// Len is the number of patterns.
func (e *Excluder) Len() int {
	if e == nil {
		return 0
	}
	return len(e.patterns)
}

// LoadDenylist adds the patterns of file (one per line, '#' starts a
// comment). A missing file adds nothing and is not an error.
func (e *Excluder) LoadDenylist(file string) error {
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if err := e.Add(line); err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
	}
	return sc.Err()
}
