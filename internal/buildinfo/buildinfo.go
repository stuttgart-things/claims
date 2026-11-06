// Package buildinfo resolves the version, commit and build date of the
// binary: from the -ldflags -X values the release builds set, falling back to
// the module and VCS data the Go toolchain embeds (debug.ReadBuildInfo), so a
// plain `go build` or `go run` still reports something useful.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Info is the resolved build information. Empty fields are unknown.
type Info struct {
	Version string
	Commit  string
	Date    string
	Dirty   bool
}

// placeholders are values that mean "not set" (older defaults, goreleaser
// snapshots without data).
var placeholders = map[string]bool{"": true, "dev": true, "none": true, "unknown": true, "(devel)": true}

func clean(s string) string {
	s = strings.TrimSpace(s)
	if placeholders[s] {
		return ""
	}
	return s
}

// Resolve merges the ldflags values with the embedded build info (bi may be
// nil). ldflags win; the embedded VCS data fills what they leave empty.
func Resolve(version, commit, date string, bi *debug.BuildInfo) Info {
	info := Info{Version: clean(version), Commit: clean(commit), Date: clean(date)}
	if bi == nil {
		return info
	}
	if info.Version == "" {
		info.Version = clean(bi.Main.Version)
	}
	var revision, vcsTime string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if info.Commit == "" && revision != "" {
		info.Commit = revision
		info.Dirty = modified
		// the toolchain's pseudo-version says +dirty too; once is enough
		info.Version = strings.TrimSuffix(info.Version, "+dirty")
	}
	if len(info.Commit) > 7 && isHex(info.Commit) {
		info.Commit = info.Commit[:7]
	}
	if info.Date == "" {
		info.Date = vcsTime
	}
	return info
}

// Current resolves the build information of the running binary.
func Current(version, commit, date string) Info {
	bi, _ := debug.ReadBuildInfo()
	return Resolve(version, commit, date, bi)
}

// Fields returns the known fields as label/value pairs, in display order.
func (i Info) Fields() [][2]string {
	var out [][2]string
	v := i.Version
	if v == "" {
		v = "dev"
	}
	out = append(out, [2]string{"Version", v})
	if i.Commit != "" {
		c := i.Commit
		if i.Dirty {
			c += " (dirty)"
		}
		out = append(out, [2]string{"Commit", c})
	}
	if i.Date != "" {
		out = append(out, [2]string{"Built", i.Date})
	}
	return out
}

// String is the one-line form: "Version: v1.2.3 | Commit: abc1234 | Built: …",
// leaving out unknown fields.
func (i Info) String() string {
	var parts []string
	for _, f := range i.Fields() {
		parts = append(parts, f[0]+": "+f[1])
	}
	return strings.Join(parts, " | ")
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
