// Package layout places the documents of a rendered order into a GitOps
// repository by the paths the order names itself.
//
// Under the source-path layout the order decides where it goes, not the CLI
// (stuttgart-things/claims#92, stuttgart-things/stuttgart-things#4296):
//
//   - a document annotated stuttgart-things.com/target-path goes to that file
//   - a document annotated stuttgart-things.com/source-path goes to that file
//     (the annotation names the file the document lives in; the repository's
//     check-xr-source-path holds it to that)
//   - a document with neither goes to the file of the document before it
//
// Paths are relative to the repository root. If a kustomization.yaml sits next
// to a written file, the file's entry is appended to its resources.
package layout

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// AnnotationSourcePath names the file a document lives in.
	AnnotationSourcePath = "stuttgart-things.com/source-path"
	// AnnotationTargetPath names the file a client writes a document to when
	// it is not the order's own file (e.g. a dev VM's ScrapeConfig).
	AnnotationTargetPath = "stuttgart-things.com/target-path"
	// AnnotationManagementCluster names the cluster that applies the order.
	AnnotationManagementCluster = "stuttgart-things.com/management-cluster"
)

// Document is one YAML document of a rendered order.
type Document struct {
	Kind        string
	Name        string
	Annotations map[string]string
	Content     string
}

// File is one file of an order: its repository-relative path and the
// documents it holds, in render order.
type File struct {
	Path      string
	Documents []Document
}

// Content is the file as written: every document starts with "---".
func (f File) Content() string {
	var sb strings.Builder
	for _, d := range f.Documents {
		sb.WriteString("---\n")
		sb.WriteString(strings.TrimSpace(d.Content))
		sb.WriteString("\n")
	}
	return sb.String()
}

var separator = regexp.MustCompile(`(?m)^---[ \t]*$`)

// SplitDocuments splits a YAML stream into its non-empty documents.
func SplitDocuments(content string) ([]Document, error) {
	var docs []Document
	for _, part := range separator.Split(content, -1) {
		var meta struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name        string            `yaml:"name"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"metadata"`
		}
		var probe interface{}
		if err := yaml.Unmarshal([]byte(part), &probe); err != nil {
			return nil, fmt.Errorf("parsing rendered document: %w", err)
		}
		if probe == nil {
			continue // empty or comments only
		}
		if err := yaml.Unmarshal([]byte(part), &meta); err != nil {
			return nil, fmt.Errorf("parsing rendered document: %w", err)
		}
		docs = append(docs, Document{
			Kind:        meta.Kind,
			Name:        meta.Metadata.Name,
			Annotations: meta.Metadata.Annotations,
			Content:     part,
		})
	}
	return docs, nil
}

// Plan maps the documents of a rendered order to files.
func Plan(content string) ([]File, error) {
	docs, err := SplitDocuments(content)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("the rendered order has no documents")
	}

	var files []File
	index := map[string]int{}
	current := ""
	for _, d := range docs {
		p := d.Annotations[AnnotationTargetPath]
		if p == "" {
			p = d.Annotations[AnnotationSourcePath]
		}
		if p == "" {
			p = current
		}
		if p == "" {
			return nil, fmt.Errorf("%s %q names no %s or %s: the source-path layout cannot place it",
				d.Kind, d.Name, AnnotationSourcePath, AnnotationTargetPath)
		}
		if err := validatePath(p); err != nil {
			return nil, fmt.Errorf("%s %q: %w", d.Kind, d.Name, err)
		}
		if i, ok := index[p]; ok {
			files[i].Documents = append(files[i].Documents, d)
		} else {
			index[p] = len(files)
			files = append(files, File{Path: p, Documents: []Document{d}})
		}
		// A target-path document is placed elsewhere; the documents after it
		// still belong to the order's own file.
		if d.Annotations[AnnotationTargetPath] == "" {
			current = p
		}
	}
	return files, nil
}

func validatePath(p string) error {
	if path.IsAbs(p) || strings.HasPrefix(p, "/") {
		return fmt.Errorf("path %q is absolute, want one relative to the repository root", p)
	}
	if clean := path.Clean(p); clean != p || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return fmt.Errorf("path %q is not a clean path inside the repository", p)
	}
	if ext := path.Ext(p); ext != ".yaml" && ext != ".yml" {
		return fmt.Errorf("path %q is not a .yaml file", p)
	}
	return nil
}

// KustomizationChange is what an order appends to one kustomization.yaml.
type KustomizationChange struct {
	Path   string   // repository-relative path of the kustomization
	Lines  []string // the appended lines, as written
	Before string
	After  string
}

// kustomizationNames are the file names kustomize reads, in its order.
var kustomizationNames = []string{"kustomization.yaml", "kustomization.yml", "Kustomization"}

// Kustomizations appends each file's entry to the kustomization next to it,
// the way the Backstage templates do: one line "  - <file>" at the end of the
// kustomization, which needs resources: to be its last key. A directory
// without a kustomization is applied as a whole and gets nothing; an entry
// that is there already is left alone. Several files in one directory make
// one change.
func Kustomizations(repoRoot string, files []File) ([]*KustomizationChange, error) {
	var changes []*KustomizationChange
	byPath := map[string]*KustomizationChange{}
	for _, f := range files {
		dir := path.Dir(f.Path)
		kPath, raw, err := findKustomization(repoRoot, dir)
		if err != nil {
			return nil, err
		}
		if kPath == "" {
			continue
		}
		if c, ok := byPath[kPath]; ok {
			raw = c.After
		}
		after, line, err := appendEntry(kPath, raw, path.Base(f.Path))
		if err != nil {
			return nil, err
		}
		if line == "" {
			continue
		}
		if c, ok := byPath[kPath]; ok {
			c.After = after
			c.Lines = append(c.Lines, line)
			continue
		}
		c := &KustomizationChange{Path: kPath, Before: raw, After: after, Lines: []string{line}}
		byPath[kPath] = c
		changes = append(changes, c)
	}
	return changes, nil
}

func findKustomization(repoRoot, dir string) (string, string, error) {
	for _, n := range kustomizationNames {
		p := path.Join(dir, n)
		data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(p)))
		if err == nil {
			return p, string(data), nil
		}
		if !os.IsNotExist(err) {
			return "", "", fmt.Errorf("reading %s: %w", p, err)
		}
	}
	return "", "", nil
}

// appendEntry returns raw with "- entry" appended to its resources, and the
// appended line; an empty line when the entry is there already.
func appendEntry(kPath, raw, entry string) (string, string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		return "", "", fmt.Errorf("parsing %s: %w", kPath, err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return "", "", fmt.Errorf("%s is not a YAML mapping", kPath)
	}
	m := root.Content[0]
	var resources *yaml.Node
	last := ""
	for i := 0; i+1 < len(m.Content); i += 2 {
		last = m.Content[i].Value
		if last == "resources" {
			resources = m.Content[i+1]
		}
	}
	if resources != nil && resources.Kind == yaml.SequenceNode {
		for _, r := range resources.Content {
			if r.Value == entry {
				return raw, "", nil
			}
		}
	}
	if resources == nil || last != "resources" || resources.Kind != yaml.SequenceNode ||
		resources.Style == yaml.FlowStyle || len(resources.Content) == 0 {
		return "", "", fmt.Errorf("%s: cannot append %q -- resources: must be the last key, a non-empty block list", kPath, entry)
	}

	indent := "  "
	for _, line := range strings.Split(raw, "\n") {
		if t := strings.TrimLeft(line, " "); strings.HasPrefix(t, "- ") {
			indent = line[:len(line)-len(t)]
		}
	}
	line := indent + "- " + entry
	after := raw
	if !strings.HasSuffix(after, "\n") {
		after += "\n"
	}
	return after + line + "\n", line, nil
}

// Write writes the files and kustomization changes below repoRoot. It
// refuses to overwrite an existing file: an order's file is new, and an
// existing one means the name is taken.
func Write(repoRoot string, files []File, changes []*KustomizationChange) error {
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(f.Path))); err == nil {
			return fmt.Errorf("%s exists already: the name is taken", f.Path)
		}
	}
	for _, f := range files {
		p := filepath.Join(repoRoot, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", path.Dir(f.Path), err)
		}
		if err := os.WriteFile(p, []byte(f.Content()), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", f.Path, err)
		}
	}
	for _, c := range changes {
		p := filepath.Join(repoRoot, filepath.FromSlash(c.Path))
		if err := os.WriteFile(p, []byte(c.After), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", c.Path, err)
		}
	}
	return nil
}
