package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/registry"
	"github.com/stuttgart-things/claims/internal/xrdelete"
)

const (
	defaultRegistryPath = "claims/registry.yaml"
	envRegistry         = "CLAIMS_REGISTRY"
	envRepo             = "CLAIMS_REPO"

	listSourceAll      = "all"
	listSourceRegistry = "registry"
	listSourceXRs      = "xrs"

	// sourceXRs marks entries read from an *-xrs directory (SOURCE column).
	sourceXRs = "xrs"
)

var (
	listRegistryPath string
	listRepo         string
	listSource       string
	listCategory     string
	listTemplate     string
	listKinds        []string
	listOutput       string
	listGitHub       string
)

var listCmd = &cobra.Command{
	Use:     "list",
	GroupID: groupClaims,
	Short:   "List claims (registry and *-xrs directories)",
	Long: `Lists the claims of a fleet repository from two places:

  registry  claims/registry.yaml, written by "claims render" (default layout)
  xrs       the XRs in clusters/<lab>/<infra>/*-xrs/*.yaml, e.g. the VM orders in
            clusters/labda/vsphere/machinery-orders-xrs/ (run-once runs left out);
            TEMPLATE shows the XR kind, PATH the file

The repository is the git repository around the current directory, or
--repo / $CLAIMS_REPO. The registry is <repo>/claims/registry.yaml, or
--registry / $CLAIMS_REGISTRY (relative paths: against the current directory if
the file exists there, else against the repository). A missing registry is fine
as long as the repository has *-xrs directories.

Without a checkout of the fleet repository (and without --repo / --registry),
or with --github owner/name, claims list reads both from GitHub at main:
--github, else $CLAIMS_GITHUB_REPO, else stuttgart-things/stuttgart-things. The
token is $GIT_TOKEN / $GITHUB_TOKEN, else the gh CLI's login.`,
	Example: `  claims list
  claims list --kind NativeVsphereVM,NativeProxmoxVM
  claims list --repo ~/projects/stuttgart-things -o json
  claims list --github stuttgart-things/stuttgart-things --kind NativeProxmoxVM
  CLAIMS_REGISTRY=/path/to/registry.yaml claims list --source registry`,
	Args: cobra.NoArgs,
	Run:  runList,
}

func init() {
	listCmd.Flags().StringVar(&listRegistryPath, "registry", "", "Path to registry.yaml (default: $CLAIMS_REGISTRY or <repo>/"+defaultRegistryPath+")")
	listCmd.Flags().StringVar(&listRegistryPath, "registry-path", "", "Path to registry.yaml (alias of --registry)")
	_ = listCmd.Flags().MarkHidden("registry-path")
	listCmd.Flags().StringVar(&listRepo, "repo", "", "Fleet repository root (default: $CLAIMS_REPO or the git repository around the current directory)")
	listCmd.Flags().StringVar(&listSource, "source", listSourceAll, "Where to read claims from: all, registry, xrs")
	listCmd.Flags().StringVar(&listCategory, "category", "", "Filter by category")
	listCmd.Flags().StringVar(&listTemplate, "template", "", "Filter by template (XRs: kind)")
	listCmd.Flags().StringSliceVar(&listKinds, "kind", nil, "Only XRs of these kinds (comma-separated or repeated, case-insensitive); leaves out registry entries")
	listCmd.Flags().StringVarP(&listOutput, "output", "o", "table", "Output format (table, json)")
	listCmd.Flags().StringVar(&listGitHub, "github", "", "Read the fleet repository owner/name from GitHub instead of a checkout (default without a checkout: $"+envFleetRepo+" or "+defaultFleetRepo+")")

	rootCmd.AddCommand(listCmd)
}

// listOptions are the resolved inputs of claims list.
type listOptions struct {
	Registry string // flag value; "" = env or default
	Repo     string // flag value; "" = env or git root of Cwd
	Cwd      string
	Getenv   func(string) string
}

// listLocation is where claims list reads from.
type listLocation struct {
	RepoRoot         string // "" when not found
	RepoExplicit     bool   // set by flag or env
	RegistryPath     string
	RegistryExplicit bool // set by flag or env: a missing file is an error

	// Remote, when set, is the GitHub repository read instead of RepoRoot;
	// its registry is claims/registry.yaml in it.
	Remote *fleetSource
}

// resolveListLocation applies flag > env > default for the repository root
// and the registry path.
func resolveListLocation(o listOptions) listLocation {
	var loc listLocation

	repo := o.Repo
	if repo == "" {
		repo = o.Getenv(envRepo)
	}
	loc.RepoExplicit = repo != ""
	if repo != "" {
		if !filepath.IsAbs(repo) {
			repo = filepath.Join(o.Cwd, repo)
		}
		loc.RepoRoot = filepath.Clean(repo)
	} else if root, err := findRepoRoot(o.Cwd); err == nil {
		loc.RepoRoot = root
	}

	reg := o.Registry
	if reg == "" {
		reg = o.Getenv(envRegistry)
	}
	loc.RegistryExplicit = reg != ""
	if reg == "" {
		reg = defaultRegistryPath
	}

	switch {
	case filepath.IsAbs(reg):
		loc.RegistryPath = reg
	case loc.RegistryExplicit && fileExists(filepath.Join(o.Cwd, reg)):
		loc.RegistryPath = filepath.Join(o.Cwd, reg)
	case loc.RepoRoot != "":
		loc.RegistryPath = filepath.Join(loc.RepoRoot, reg)
	default:
		loc.RegistryPath = filepath.Join(o.Cwd, reg)
	}
	return loc
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// wantRemoteList: --github, or nothing local to read and nothing that names a
// local place (--repo, --registry or their env vars).
func wantRemoteList(loc listLocation, github string) bool {
	if github != "" {
		return true
	}
	if loc.RepoExplicit || loc.RegistryExplicit {
		return false
	}
	if fileExists(loc.RegistryPath) {
		return false
	}
	if loc.RepoRoot != "" {
		if dirs, err := xrdelete.XRSDirs(loc.RepoRoot); err == nil && len(dirs) > 0 {
			return false
		}
	}
	return true
}

// loadListRegistry reads the registry from the checkout or the remote tree.
func loadListRegistry(loc listLocation) (*registry.ClaimRegistry, error) {
	if loc.Remote == nil {
		return registry.Load(loc.RegistryPath)
	}
	data, err := fs.ReadFile(loc.Remote.FS, defaultRegistryPath)
	if err != nil {
		return nil, err
	}
	return registry.Parse(data)
}

// collectClaims reads the registry and/or the *-xrs directories.
func collectClaims(loc listLocation, source string) ([]registry.ClaimEntry, error) {
	switch source {
	case listSourceAll, listSourceRegistry, listSourceXRs:
	default:
		return nil, fmt.Errorf("unknown --source %q (want %s, %s or %s)", source, listSourceAll, listSourceRegistry, listSourceXRs)
	}

	var entries []registry.ClaimEntry
	foundRegistry, foundXRs := false, false

	if source != listSourceXRs {
		reg, err := loadListRegistry(loc)
		switch {
		case err == nil:
			foundRegistry = true
			entries = append(entries, reg.Claims...)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		case loc.RegistryExplicit || source == listSourceRegistry:
			return nil, fmt.Errorf("no claims registry at %s\n%s", loc.RegistryPath, listLocationHint)
		}
	}

	var xrsFS fs.FS
	switch {
	case loc.Remote != nil:
		xrsFS = loc.Remote.FS
	case loc.RepoRoot != "":
		xrsFS = os.DirFS(loc.RepoRoot)
	}
	if source != listSourceRegistry && xrsFS != nil {
		dirs, err := xrdelete.XRSDirsFS(xrsFS)
		if err != nil {
			return nil, err
		}
		foundXRs = len(dirs) > 0
		xrs, err := xrdelete.ListFS(xrsFS)
		if err != nil {
			return nil, err
		}
		for _, x := range xrs {
			entries = append(entries, registry.ClaimEntry{
				Name:      x.Name,
				Template:  x.Kind,
				Namespace: x.Namespace,
				Source:    sourceXRs,
				Path:      x.File,
			})
		}
	}

	if !foundRegistry && !foundXRs {
		if loc.Remote != nil {
			return nil, fmt.Errorf("neither a claims registry (%s) nor *-xrs directories in %s", defaultRegistryPath, loc.Remote)
		}
		where := loc.RegistryPath
		if loc.RepoRoot == "" {
			return nil, fmt.Errorf("not in a git repository and no claims registry at %s\n%s", where, listLocationHint)
		}
		return nil, fmt.Errorf("neither a claims registry (%s) nor *-xrs directories in %s\n%s", where, loc.RepoRoot, listLocationHint)
	}
	return entries, nil
}

const listLocationHint = `Run claims list inside the fleet repository, or point it there:
  --repo <dir>        or CLAIMS_REPO=<dir>       (repository root)
  --registry <file>   or CLAIMS_REGISTRY=<file>  (registry.yaml)
  --github <o/name>   or CLAIMS_GITHUB_REPO=...  (read from GitHub, no checkout)`

func runList(cmd *cobra.Command, args []string) {
	cwd, err := os.Getwd()
	if err != nil {
		printError(err)
		os.Exit(1)
	}
	loc := resolveListLocation(listOptions{
		Registry: listRegistryPath,
		Repo:     listRepo,
		Cwd:      cwd,
		Getenv:   os.Getenv,
	})
	if wantRemoteList(loc, listGitHub) {
		src, err := openRemoteFleet(fleetRepoName(listGitHub), "main")
		if err != nil {
			printError(fmt.Errorf("%w\n%s", err, listLocationHint))
			os.Exit(1)
		}
		loc = listLocation{Remote: src, RegistryPath: defaultRegistryPath}
	}

	all, err := collectClaims(loc, listSource)
	if err != nil {
		printError(err)
		os.Exit(1)
	}

	reg := &registry.ClaimRegistry{Claims: all}
	entries := registry.FilterEntries(reg, listCategory, listTemplate)
	entries, dropped := filterKinds(entries, listKinds)
	if dropped > 0 {
		fmt.Fprintf(os.Stderr, "Note: --kind leaves out %d registry entr%s (they have no kind)\n", dropped, map[bool]string{true: "y", false: "ies"}[dropped == 1])
	}

	if len(entries) == 0 {
		fmt.Println("No claims found.")
		return
	}

	switch listOutput {
	case "json":
		printJSON(entries)
	default:
		printTable(entries)
	}
}

// filterKinds keeps the XR entries whose kind is in kinds (case-insensitive;
// empty kinds keeps everything). Registry entries have no kind and are left
// out; dropped counts them.
func filterKinds(entries []registry.ClaimEntry, kinds []string) (kept []registry.ClaimEntry, dropped int) {
	want := map[string]bool{}
	for _, k := range kinds {
		if k = strings.TrimSpace(k); k != "" {
			want[strings.ToLower(k)] = true
		}
	}
	if len(want) == 0 {
		return entries, 0
	}
	for _, e := range entries {
		if e.Source != sourceXRs {
			dropped++
			continue
		}
		if want[strings.ToLower(e.Template)] {
			kept = append(kept, e)
		}
	}
	return kept, dropped
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printTable(entries []registry.ClaimEntry) {
	writeTable(os.Stdout, entries)
}

func writeTable(out io.Writer, entries []registry.ClaimEntry) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tTEMPLATE\tCATEGORY\tNAMESPACE\tSTATUS\tCREATED BY\tSOURCE\tPATH")
	_, _ = fmt.Fprintln(w, "----\t--------\t--------\t---------\t------\t----------\t------\t----")

	for _, e := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Name, dash(e.Template), dash(e.Category), dash(e.Namespace), dash(e.Status), dash(e.CreatedBy), dash(e.Source), dash(e.Path))
	}

	_ = w.Flush()
}

func printJSON(entries []registry.ClaimEntry) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, errorStyle.Render(fmt.Sprintf("Error marshalling JSON: %v", err)))
		os.Exit(1)
	}
	fmt.Println(string(data))
}
