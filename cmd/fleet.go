package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/stuttgart-things/claims/internal/ghclient"
	"github.com/stuttgart-things/claims/internal/ghfs"
	"github.com/stuttgart-things/claims/internal/xrdelete"
)

// The fleet repository (claims#107): claims change and claims list read the
// *-xrs claims from a local checkout when the current directory is in one,
// else straight from GitHub -- no clone needed.

const (
	// defaultFleetRepo is read when there is no checkout and nothing names one.
	defaultFleetRepo = "stuttgart-things/stuttgart-things"
	// envFleetRepo overrides defaultFleetRepo.
	envFleetRepo = "CLAIMS_GITHUB_REPO"
)

// fleetSource is where the claims are read from.
type fleetSource struct {
	FS   fs.FS
	Root string // local checkout; "" when remote
	Repo string // owner/name when remote
	Ref  string // when remote
}

func (s *fleetSource) remote() bool { return s.Root == "" }

func (s *fleetSource) String() string {
	if s.remote() {
		return fmt.Sprintf("github.com/%s@%s", s.Repo, s.Ref)
	}
	return s.Root
}

// fleetRepoName is the repository to read remotely: the flag, $CLAIMS_GITHUB_REPO,
// or the default.
func fleetRepoName(flag string) string {
	if flag != "" {
		return flag
	}
	if env := os.Getenv(envFleetRepo); env != "" {
		return env
	}
	return defaultFleetRepo
}

// localFleet returns the checkout around dir when it holds *-xrs directories:
// a checkout of another repository (claims itself, say) is not the fleet.
func localFleet(dir string) (*fleetSource, bool) {
	root, err := findRepoRoot(dir)
	if err != nil {
		return nil, false
	}
	fsys := os.DirFS(root)
	if dirs, err := xrdelete.XRSDirsFS(fsys); err != nil || len(dirs) == 0 {
		return nil, false
	}
	return &fleetSource{FS: fsys, Root: root}, true
}

// openFleet is the local checkout around the current directory, else the
// GitHub repository repo (see fleetRepoName) at ref.
func openFleet(repo, ref string) (*fleetSource, error) {
	if src, ok := localFleet("."); ok {
		return src, nil
	}
	return openRemoteFleet(fleetRepoName(repo), ref)
}

// openRemoteFleet reads a repository from GitHub; a variable so tests can
// keep the network out.
var openRemoteFleet = func(repo, ref string) (*fleetSource, error) {
	client, err := ghclient.FromEnv("")
	if err != nil {
		return nil, err
	}
	g, err := ghfs.New(context.Background(), client, repo, ref)
	if err != nil {
		return nil, err
	}
	// Every caller scans the *-xrs directories or reads one file in them:
	// fetch those files in parallel up front instead of one by one.
	if err := g.Prefetch(xrsFiles(g), 8); err != nil {
		return nil, err
	}
	return &fleetSource{FS: g, Repo: g.Repo(), Ref: g.Ref()}, nil
}

// xrsFiles lists the .yaml files of the *-xrs directories.
func xrsFiles(fsys fs.FS) []string {
	dirs, _ := xrdelete.XRSDirsFS(fsys)
	var files []string
	for _, d := range dirs {
		m, _ := fs.Glob(fsys, d+"/*.yaml")
		files = append(files, m...)
	}
	return files
}
