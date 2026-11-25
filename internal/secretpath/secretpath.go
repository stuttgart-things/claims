// Package secretpath maps a Vault KV v2 entry onto its SOPS file in the fleet
// repository and back (claims#122, crossplane-configurations#572):
//
//	secrets/<lab>/<mount>/<path>.enc.yaml  <=>  <mount>/<path> in the lab's Vault
//
// The mapping is 1:1 and needs no configuration. It also derives the
// metadata.name of the Kubernetes Secret the file holds (see SecretName).
package secretpath

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	// Root is the directory of the SOPS files, relative to the repository root.
	Root = "secrets"
	// Suffix is the extension of every SOPS file.
	Suffix = ".enc.yaml"
	// DefaultNamespace is the namespace of the Secrets in phase 1.
	DefaultNamespace = "vault-sync"
)

// Ref is one secret: a KV v2 entry <Mount>/<Path> in the Vault of Lab.
type Ref struct {
	Lab   string
	Mount string
	Path  string // below the mount, no leading/trailing slash
}

// VaultPath is <mount>/<path>.
func (r Ref) VaultPath() string { return r.Mount + "/" + r.Path }

// File is secrets/<lab>/<mount>/<path>.enc.yaml (slash-separated, relative
// to the repository root).
func (r Ref) File() string {
	return path.Join(Root, r.Lab, r.Mount, r.Path) + Suffix
}

// String is <lab>:<mount>/<path>.
func (r Ref) String() string { return r.Lab + ":" + r.VaultPath() }

var labPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidLab reports whether lab is usable as a directory and a Secret name part.
func ValidLab(lab string) error {
	if !labPattern.MatchString(lab) {
		return fmt.Errorf("lab %q: use lowercase letters, digits and '-'", lab)
	}
	return nil
}

// validSegment rejects what cannot be a path segment on both sides.
func validSegment(s string) error {
	switch {
	case s == "":
		return errors.New("empty segment (double or trailing slash)")
	case s == "." || s == "..":
		return fmt.Errorf("segment %q is not allowed", s)
	case strings.ContainsAny(s, "\\\x00"):
		return fmt.Errorf("segment %q contains a backslash or NUL", s)
	}
	return nil
}

// ParseVault parses "<mount>/<path>" of the Vault of lab.
func ParseVault(lab, vaultPath string) (Ref, error) {
	if err := ValidLab(lab); err != nil {
		return Ref{}, err
	}
	p := strings.TrimPrefix(strings.TrimSpace(vaultPath), "/")
	if strings.HasSuffix(p, "/") {
		return Ref{}, fmt.Errorf("vault path %q names a directory, not a secret", vaultPath)
	}
	mount, rest, ok := strings.Cut(p, "/")
	if !ok || rest == "" {
		return Ref{}, fmt.Errorf("vault path %q: expected <mount>/<path>, e.g. kv/machinery-dev/argocd-oidc", vaultPath)
	}
	for _, s := range strings.Split(p, "/") {
		if err := validSegment(s); err != nil {
			return Ref{}, fmt.Errorf("vault path %q: %w", vaultPath, err)
		}
	}
	// KV v2's API paths: <mount>/data/... is what the CLI hides; a user who
	// pastes the API path means the entry below it.
	if strings.HasPrefix(rest, "data/") || strings.HasPrefix(rest, "metadata/") {
		return Ref{}, fmt.Errorf("vault path %q: use the logical path (%s/<path>), not the API path with data/ or metadata/", vaultPath, mount)
	}
	return Ref{Lab: lab, Mount: mount, Path: rest}, nil
}

// ParseFile parses a repository-relative, slash-separated file path
// secrets/<lab>/<mount>/<path>.enc.yaml.
func ParseFile(file string) (Ref, error) {
	f := strings.TrimPrefix(path.Clean(strings.ReplaceAll(file, "\\", "/")), "./")
	if !strings.HasSuffix(f, Suffix) {
		return Ref{}, fmt.Errorf("%s: a SOPS secret file ends in %s", file, Suffix)
	}
	rest, ok := strings.CutPrefix(f, Root+"/")
	if !ok {
		return Ref{}, fmt.Errorf("%s: not below %s/ of the repository (layout: %s/<lab>/<mount>/<path>%s)", file, Root, Root, Suffix)
	}
	lab, vp, ok := strings.Cut(strings.TrimSuffix(rest, Suffix), "/")
	if !ok {
		return Ref{}, fmt.Errorf("%s: layout is %s/<lab>/<mount>/<path>%s", file, Root, Suffix)
	}
	r, err := ParseVault(lab, vp)
	if err != nil {
		return Ref{}, fmt.Errorf("%s: %w", file, err)
	}
	return r, nil
}

// TreeRef is a directory: every entry below <Mount>/<Prefix> of Lab.
type TreeRef struct {
	Lab    string
	Mount  string
	Prefix string // "" = the whole mount; else no leading/trailing slash
}

// VaultPath is <mount>/<prefix>/ (always ending in a slash).
func (t TreeRef) VaultPath() string {
	if t.Prefix == "" {
		return t.Mount + "/"
	}
	return t.Mount + "/" + t.Prefix + "/"
}

// Dir is secrets/<lab>/<mount>[/<prefix>].
func (t TreeRef) Dir() string { return path.Join(Root, t.Lab, t.Mount, t.Prefix) }

// Contains reports whether r lies below t.
func (t TreeRef) Contains(r Ref) bool {
	if r.Lab != t.Lab || r.Mount != t.Mount {
		return false
	}
	return t.Prefix == "" || strings.HasPrefix(r.Path, t.Prefix+"/")
}

// ParseVaultTree parses "<mount>/" or "<mount>/<prefix>/" (a trailing slash
// is optional here).
func ParseVaultTree(lab, vaultPath string) (TreeRef, error) {
	if err := ValidLab(lab); err != nil {
		return TreeRef{}, err
	}
	p := strings.Trim(strings.TrimSpace(vaultPath), "/")
	if p == "" {
		return TreeRef{}, fmt.Errorf("vault path %q: name at least the mount, e.g. kv/", vaultPath)
	}
	for _, s := range strings.Split(p, "/") {
		if err := validSegment(s); err != nil {
			return TreeRef{}, fmt.Errorf("vault path %q: %w", vaultPath, err)
		}
	}
	mount, prefix, _ := strings.Cut(p, "/")
	return TreeRef{Lab: lab, Mount: mount, Prefix: prefix}, nil
}

// ParseDir parses a repository-relative directory secrets/<lab>/<mount>[/<prefix>].
func ParseDir(dir string) (TreeRef, error) {
	d := strings.TrimPrefix(path.Clean(strings.ReplaceAll(dir, "\\", "/")), "./")
	rest, ok := strings.CutPrefix(d, Root+"/")
	if !ok {
		return TreeRef{}, fmt.Errorf("%s: not below %s/<lab>/<mount> of the repository", dir, Root)
	}
	lab, vp, ok := strings.Cut(rest, "/")
	if !ok {
		return TreeRef{}, fmt.Errorf("%s: name at least %s/<lab>/<mount>", dir, Root)
	}
	t, err := ParseVaultTree(lab, vp)
	if err != nil {
		return TreeRef{}, fmt.Errorf("%s: %w", dir, err)
	}
	return t, nil
}

// --- metadata.name -----------------------------------------------------------

// maxNameLen is the limit of a DNS-1123 subdomain (a Secret's name).
const maxNameLen = 253

// labelPattern is one DNS-1123 label without "--" (see SecretName).
var labelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// SecretName is the metadata.name of the Secret for r. The rule:
//
//  1. Clean form: when every segment of <lab>/<mount>/<path> is a DNS-1123
//     label (lowercase letters, digits, inner '-'; at most 63 characters)
//     without "--", the name is the segments joined by '.':
//     labul/kv/machinery-dev/argocd-oidc -> labul.kv.machinery-dev.argocd-oidc
//  2. Hashed form, for everything else (uppercase, '_', '.', "--", other
//     characters, too long): the lowercased path with every run of other
//     characters turned into a single '-', segments joined by '.', then
//     "--" and the first 10 hex digits of sha256("<lab>/<mount>/<path>"):
//     labul/kv/App_Creds -> labul.kv.app-creds--<hash>
//
// Deterministic (depends on the path only). Collision-free: a clean name
// never contains "--" and a hashed one always does, so the two forms never
// meet; clean names are injective because '.' cannot occur in a clean
// segment; hashed names carry 40 bits of the exact path. The result is
// always a valid DNS-1123 subdomain of at most 253 characters.
func SecretName(r Ref) string {
	full := r.Lab + "/" + r.Mount + "/" + r.Path
	segs := strings.Split(full, "/")
	clean := true
	for _, s := range segs {
		if len(s) > 63 || !labelPattern.MatchString(s) || strings.Contains(s, "--") {
			clean = false
			break
		}
	}
	if clean {
		if name := strings.Join(segs, "."); len(name) <= maxNameLen {
			return name
		}
	}
	sum := sha256.Sum256([]byte(full))
	suffix := "--" + hex.EncodeToString(sum[:])[:10]
	var parts []string
	for _, s := range segs {
		if p := slug(s); p != "" {
			parts = append(parts, p)
		}
	}
	name := strings.Join(parts, ".")
	if max := maxNameLen - len(suffix); len(name) > max {
		name = strings.TrimRight(name[:max], ".-")
	}
	if name == "" {
		return "s" + suffix
	}
	return name + suffix
}

// slug lowercases s, turns runs of anything but [a-z0-9] into one '-' and
// trims '-' at both ends; at most 63 characters.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}
