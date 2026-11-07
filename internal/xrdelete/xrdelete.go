// Package xrdelete is the file half of deleting a claim (an XR) from an
// *-xrs directory of the fleet repository, file first: remove the claim's
// file, the one-shot runs that apply to it and their kustomization.yaml
// entries, then open the pull request delete-claim/<name>.
//
// The rules mirror hack/claim-deletion.py in stuttgart-things/stuttgart-things
// (`check` / `remove`), which the pull request gate (pr-claim-delete.yaml)
// runs again on the result; after the merge delete-claim-xr.yaml deletes the
// object. This package never talks to a cluster.
//
//   - the file is a repository-relative .yaml in clusters/<lab>/<infra>/<x>-xrs/
//   - it holds exactly one XR, the claim asked for, whose
//     stuttgart-things.com/source-path annotation is the file's own path
//   - ClusterStacks are refused (hack/teardown-clusterstack.sh is that job)
//   - run-once ScheduledRuns whose stuttgart-things.com/applies names the
//     claim go with it
//   - when the directory has a kustomization.yaml, it lists each removed file
//     exactly once, and those lines are removed
package xrdelete

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	AnnotationSourcePath = "stuttgart-things.com/source-path"
	AnnotationApplies    = "stuttgart-things.com/applies"
	LabelRunOnce         = "stuttgart-things.com/run-once"
	kustomizationFile    = "kustomization.yaml"
)

// Refused kinds are not deleted through this path.
var Refused = map[string]bool{"ClusterStack": true}

var (
	nameRE   = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	xrsDirRE = regexp.MustCompile(`^clusters/[^/]+/[^/]+/[^/]+-xrs$`)
)

// XR is one claim found in a file.
type XR struct {
	APIVersion  string
	Kind        string
	Name        string
	Namespace   string
	Annotations map[string]string
	Labels      map[string]string
	File        string // repository-relative, slash-separated
}

// Target is the Kind/namespace/name form of stuttgart-things.com/applies.
func (x XR) Target() string { return x.Kind + "/" + x.Namespace + "/" + x.Name }

// SourcePath is the claim's stuttgart-things.com/source-path annotation.
func (x XR) SourcePath() string { return x.Annotations[AnnotationSourcePath] }

func (x XR) isRunOnce() bool {
	return x.Kind == "ScheduledRun" && x.Labels[LabelRunOnce] == "true" && x.SourcePath() == x.File
}

// Removal is what deleting one claim changes.
type Removal struct {
	Claim XR
	// Runs are the run files that apply to the claim (removed with it).
	Runs []string
	// Kustomization is the directory's kustomization.yaml, "" when the
	// directory has none (orders directories: Flux applies every file).
	Kustomization string
	// KustomizationAfter is its content without the removed entries.
	KustomizationAfter string
	// RemovedEntries are the kustomization lines that go.
	RemovedEntries []string
}

// Files returns the claim file and the run files.
func (r *Removal) Files() []string { return append([]string{r.Claim.File}, r.Runs...) }

// ParseXRs returns the XRs (apiVersion group *.stuttgart-things.com) in a
// multi-document YAML file.
func ParseXRs(data []byte, file string) ([]XR, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var out []XR
	for {
		var doc struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Metadata   struct {
				Name        string            `yaml:"name"`
				Namespace   string            `yaml:"namespace"`
				Annotations map[string]string `yaml:"annotations"`
				Labels      map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
		}
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", file, err)
		}
		group := strings.SplitN(doc.APIVersion, "/", 2)[0]
		if !strings.HasSuffix(group, ".stuttgart-things.com") {
			continue
		}
		ns := doc.Metadata.Namespace
		if ns == "" {
			ns = "default"
		}
		out = append(out, XR{
			APIVersion:  doc.APIVersion,
			Kind:        doc.Kind,
			Name:        doc.Metadata.Name,
			Namespace:   ns,
			Annotations: doc.Metadata.Annotations,
			Labels:      doc.Metadata.Labels,
			File:        file,
		})
	}
}

// XRSDirs lists the *-xrs directories of the repository.
func XRSDirs(repoRoot string) ([]string, error) { return XRSDirsFS(os.DirFS(repoRoot)) }

// XRSDirsFS lists the *-xrs directories of a repository tree (a checkout via
// os.DirFS, or a GitHub ref via ghfs).
func XRSDirsFS(fsys fs.FS) ([]string, error) {
	matches, err := fs.Glob(fsys, "clusters/*/*/*-xrs")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, m := range matches {
		if fi, err := fs.Stat(fsys, m); err == nil && fi.IsDir() {
			dirs = append(dirs, m)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// yamlFiles returns the .yaml files of dir, repository-relative.
func yamlFiles(fsys fs.FS, dir string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			files = append(files, path.Join(dir, e.Name()))
		}
	}
	return files, nil
}

// Scan returns the deletable claims of all *-xrs directories: files holding
// exactly one XR whose source-path is the file, not a run-once ScheduledRun,
// and not a refused kind.
func Scan(repoRoot string) ([]XR, error) { return ScanFS(os.DirFS(repoRoot)) }

// ScanFS is Scan over a repository tree.
func ScanFS(fsys fs.FS) ([]XR, error) {
	dirs, err := XRSDirsFS(fsys)
	if err != nil {
		return nil, err
	}
	var out []XR
	for _, d := range dirs {
		files, err := yamlFiles(fsys, d)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if path.Base(file) == kustomizationFile {
				continue
			}
			data, err := fs.ReadFile(fsys, file)
			if err != nil {
				return nil, err
			}
			xrs, err := ParseXRs(data, file)
			if err != nil || len(xrs) != 1 {
				continue
			}
			x := xrs[0]
			if x.SourcePath() != file || x.isRunOnce() || Refused[x.Kind] {
				continue
			}
			out = append(out, x)
		}
	}
	return out, nil
}

// List returns every XR of the *-xrs directories for display (claims list):
// unlike Scan it keeps files with several XRs, refused kinds and XRs whose
// source-path does not match, and leaves out only the run-once
// ScheduledRuns (they are runs, not claims). Unparsable files are skipped.
func List(repoRoot string) ([]XR, error) { return ListFS(os.DirFS(repoRoot)) }

// ListFS is List over a repository tree.
func ListFS(fsys fs.FS) ([]XR, error) {
	dirs, err := XRSDirsFS(fsys)
	if err != nil {
		return nil, err
	}
	var out []XR
	for _, d := range dirs {
		files, err := yamlFiles(fsys, d)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if path.Base(file) == kustomizationFile {
				continue
			}
			data, err := fs.ReadFile(fsys, file)
			if err != nil {
				return nil, err
			}
			xrs, err := ParseXRs(data, file)
			if err != nil {
				continue
			}
			for _, x := range xrs {
				if !x.isRunOnce() {
					out = append(out, x)
				}
			}
		}
	}
	return out, nil
}

// Find returns the claim kind/namespace/name among the deletable claims.
func Find(repoRoot, kind, name, namespace string) (XR, error) {
	return FindFS(os.DirFS(repoRoot), kind, name, namespace)
}

// FindFS is Find over a repository tree.
func FindFS(fsys fs.FS, kind, name, namespace string) (XR, error) {
	if namespace == "" {
		namespace = "default"
	}
	all, err := ScanFS(fsys)
	if err != nil {
		return XR{}, err
	}
	var hits []XR
	for _, x := range all {
		if x.Name == name && x.Namespace == namespace && (kind == "" || x.Kind == kind) {
			hits = append(hits, x)
		}
	}
	switch len(hits) {
	case 0:
		return XR{}, fmt.Errorf("no deletable claim %s %s/%s in the *-xrs directories (one XR per file, source-path = the file)", orAny(kind), namespace, name)
	case 1:
		return hits[0], nil
	default:
		var files []string
		for _, h := range hits {
			files = append(files, h.Kind+" in "+h.File)
		}
		return XR{}, fmt.Errorf("%s/%s is ambiguous: %s; pass --kind or --source-path", namespace, name, strings.Join(files, ", "))
	}
}

func orAny(kind string) string {
	if kind == "" {
		return "(any kind)"
	}
	return kind
}

// CheckPath validates a source path like claim-deletion.py check_path, minus
// the PLANES lookup (the pull request gate does that).
func CheckPath(sourcePath string) error {
	p := sourcePath
	if p == "" || path.IsAbs(p) || strings.Contains(p, "\\") || path.Clean(p) != p || strings.HasPrefix(p, "../") || path.Ext(p) != ".yaml" {
		return fmt.Errorf("%q is not a repository-relative .yaml path", sourcePath)
	}
	if !xrsDirRE.MatchString(path.Dir(p)) {
		return fmt.Errorf("%q is not in an *-xrs directory (clusters/<lab>/<infra>/<x>-xrs/)", sourcePath)
	}
	if path.Base(p) == kustomizationFile {
		return errors.New("refusing to delete a kustomization.yaml")
	}
	return nil
}

// Plan checks that the claim kind/namespace/name lives alone in sourcePath
// and works out what deleting it changes. Nothing is written.
func Plan(repoRoot, kind, name, namespace, sourcePath string) (*Removal, error) {
	return PlanFS(os.DirFS(repoRoot), kind, name, namespace, sourcePath)
}

// PlanFS is Plan over a repository tree.
func PlanFS(fsys fs.FS, kind, name, namespace, sourcePath string) (*Removal, error) {
	if namespace == "" {
		namespace = "default"
	}
	if !nameRE.MatchString(name) || !nameRE.MatchString(namespace) {
		return nil, fmt.Errorf("%q / %q is not a valid object name / namespace", name, namespace)
	}
	if Refused[kind] {
		return nil, fmt.Errorf("%s is not deleted through this path (yet); see machinery-xrs/README.md", kind)
	}
	if err := CheckPath(sourcePath); err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(fsys, sourcePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s does not exist", sourcePath)
		}
		return nil, err
	}
	xrs, err := ParseXRs(data, sourcePath)
	if err != nil {
		return nil, err
	}
	if len(xrs) != 1 {
		return nil, fmt.Errorf("%s holds %d XRs; only a file with exactly one claim is deleted whole -- split it first", sourcePath, len(xrs))
	}
	x := xrs[0]
	if x.Kind != kind || x.Name != name || x.Namespace != namespace {
		return nil, fmt.Errorf("%s holds %s, not %s/%s/%s", sourcePath, x.Target(), kind, namespace, name)
	}
	if x.SourcePath() != sourcePath {
		return nil, fmt.Errorf("%s: %s is %q, not the file's own path", sourcePath, AnnotationSourcePath, x.SourcePath())
	}

	r := &Removal{Claim: x}
	dir := path.Dir(sourcePath)
	r.Runs, err = runsFor(fsys, dir, x)
	if err != nil {
		return nil, err
	}

	kRel := path.Join(dir, kustomizationFile)
	kData, err := fs.ReadFile(fsys, kRel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r, nil
	case err != nil:
		return nil, err
	}
	r.Kustomization = kRel
	lines := strings.Split(string(kData), "\n")
	removed := map[int]bool{}
	for _, f := range r.Files() {
		re := entryRE(path.Base(f))
		var hits []int
		for i, l := range lines {
			if re.MatchString(l) {
				hits = append(hits, i)
			}
		}
		if len(hits) != 1 {
			return nil, fmt.Errorf("%s lists %s %d times, expected once", kRel, path.Base(f), len(hits))
		}
		removed[hits[0]] = true
		r.RemovedEntries = append(r.RemovedEntries, strings.TrimSpace(lines[hits[0]]))
	}
	var kept []string
	for i, l := range lines {
		if !removed[i] {
			kept = append(kept, l)
		}
	}
	r.KustomizationAfter = strings.Join(kept, "\n")
	return r, nil
}

func runsFor(fsys fs.FS, dir string, claim XR) ([]string, error) {
	files, err := yamlFiles(fsys, dir)
	if err != nil {
		return nil, err
	}
	var runs []string
	for _, file := range files {
		if file == claim.File {
			continue
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		xrs, err := ParseXRs(data, file)
		if err != nil || len(xrs) != 1 {
			continue
		}
		if run := xrs[0]; run.isRunOnce() && run.Annotations[AnnotationApplies] == claim.Target() {
			runs = append(runs, file)
		}
	}
	sort.Strings(runs)
	return runs, nil
}

func entryRE(filename string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*-\s+(\./)?` + regexp.QuoteMeta(filename) + `\s*(#.*)?$`)
}

// Apply writes the kustomization.yaml without the entries and, when
// removeFiles is set, deletes the claim and run files. (With git, the
// files are removed by staging their removal instead.)
func Apply(repoRoot string, r *Removal, removeFiles bool) error {
	if r.Kustomization != "" {
		kp := filepath.Join(repoRoot, filepath.FromSlash(r.Kustomization))
		fi, err := os.Stat(kp)
		if err != nil {
			return err
		}
		if err := os.WriteFile(kp, []byte(r.KustomizationAfter), fi.Mode().Perm()); err != nil {
			return fmt.Errorf("writing %s: %w", r.Kustomization, err)
		}
	}
	if !removeFiles {
		return nil
	}
	for _, f := range r.Files() {
		if err := os.Remove(filepath.Join(repoRoot, filepath.FromSlash(f))); err != nil {
			return fmt.Errorf("removing %s: %w", f, err)
		}
	}
	return nil
}

// Convention is the branch/commit/title/label the deletion pipeline expects
// (dispatch-delete-claim.yaml): pr-claim-delete.yaml gates only
// delete-claim/* branches, and delete-claim-xr.yaml deletes the object only
// after such a pull request is merged.
type Convention struct {
	Branch string
	Commit string
	Title  string
	Label  string
	Body   string
}

// ConventionFor builds the pull request for r.
func ConventionFor(r *Removal, requestedBy, reason string) Convention {
	c := r.Claim
	if requestedBy == "" {
		requestedBy = "n/a"
	}
	var b strings.Builder
	b.WriteString("Removes the claim file for:\n\n")
	b.WriteString("| | |\n|---|---|\n")
	fmt.Fprintf(&b, "| Kind | `%s` |\n", c.Kind)
	fmt.Fprintf(&b, "| Name | `%s/%s` |\n", c.Namespace, c.Name)
	fmt.Fprintf(&b, "| File | `%s` |\n", c.File)
	fmt.Fprintf(&b, "| Requested by | %s |\n\n", requestedBy)
	if reason != "" {
		fmt.Fprintf(&b, "**Reason:** %s\n\n", reason)
	}
	if len(r.Runs) > 0 {
		b.WriteString("One-shot runs that apply to the claim go with it:\n\n")
		for _, run := range r.Runs {
			fmt.Fprintf(&b, "- `%s`\n", run)
		}
		b.WriteString("\n")
	}
	b.WriteString("`pr-claim-delete.yaml` checks that this deletes exactly this claim\n" +
		"(with its runs) and nothing else. Once it is merged, `delete-claim-xr.yaml` waits for\n" +
		"Flux to apply the revision and then deletes the object on the\nmanagement cluster.\n\n")
	b.WriteString("---\n*Opened with `claims delete`*\n")
	return Convention{
		Branch: "delete-claim/" + c.Name,
		Commit: fmt.Sprintf("Delete claim %s/%s", c.Kind, c.Name),
		Title:  fmt.Sprintf("Delete claim: %s/%s", c.Kind, c.Name),
		Label:  "delete-claim",
		Body:   b.String(),
	}
}
