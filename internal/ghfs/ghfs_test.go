package ghfs_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/google/go-github/v93/github"

	"github.com/stuttgart-things/claims/internal/ghfs"
)

// fakeGitHub serves the git trees and blobs API for one repository.
type fakeGitHub struct {
	files     map[string]string // path -> content
	truncated bool
	status    int // non-zero: every request answers this
	blobCalls atomic.Int32
}

func sha(p string) string { return "sha-" + strings.ReplaceAll(p, "/", "_") }

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(`{"message":"nope"}`))
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/repos/acme/fleet/git/trees/"):
		if r.URL.Path != "/repos/acme/fleet/git/trees/main" || r.URL.Query().Get("recursive") == "" {
			http.NotFound(w, r)
			return
		}
		dirs := map[string]bool{}
		var entries []map[string]any
		for p, c := range f.files {
			entries = append(entries, map[string]any{"path": p, "type": "blob", "sha": sha(p), "size": len(c)})
			for d := p; strings.Contains(d, "/"); {
				d = d[:strings.LastIndex(d, "/")]
				dirs[d] = true
			}
		}
		for d := range dirs {
			entries = append(entries, map[string]any{"path": d, "type": "tree", "sha": sha(d)})
		}
		entries = append(entries, map[string]any{"path": "vendor/sub", "type": "commit", "sha": "x"})
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": "root", "tree": entries, "truncated": f.truncated})
	case strings.HasPrefix(r.URL.Path, "/repos/acme/fleet/git/blobs/"):
		f.blobCalls.Add(1)
		want := strings.TrimPrefix(r.URL.Path, "/repos/acme/fleet/git/blobs/")
		for p, c := range f.files {
			if sha(p) == want {
				_, _ = w.Write([]byte(c))
				return
			}
		}
		http.NotFound(w, r)
	default:
		http.NotFound(w, r)
	}
}

func newFS(t *testing.T, f *fakeGitHub, ref string) (*ghfs.FS, error) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	base := srv.URL + "/"
	client, err := github.NewClient(github.WithURLs(&base, &base))
	if err != nil {
		t.Fatal(err)
	}
	return ghfs.New(context.Background(), client, "acme/fleet", ref)
}

var fleet = map[string]string{
	"README.md": "# fleet\n",
	"clusters/labda/vsphere/machinery-xrs/a.yaml":             "kind: A\n",
	"clusters/labda/vsphere/machinery-xrs/kustomization.yaml": "resources: []\n",
	"clusters/labul/proxmox/orders-xrs/b.yaml":                "kind: B\n",
}

func TestFS(t *testing.T) {
	f := &fakeGitHub{files: fleet}
	g, err := newFS(t, f, "main")
	if err != nil {
		t.Fatal(err)
	}
	if g.Repo() != "acme/fleet" || g.Ref() != "main" {
		t.Errorf("Repo/Ref = %s/%s", g.Repo(), g.Ref())
	}
	var paths []string
	for p := range fleet {
		paths = append(paths, p)
	}
	if err := fstest.TestFS(g, paths...); err != nil {
		t.Fatal(err)
	}

	dirs, err := fs.Glob(g, "clusters/*/*/*-xrs")
	if err != nil || len(dirs) != 2 {
		t.Fatalf("glob = %v, %v", dirs, err)
	}
	if _, err := fs.Stat(g, "vendor/sub"); err == nil {
		t.Error("a submodule shows up as an entry")
	}
	if _, err := fs.ReadFile(g, "missing.yaml"); !strings.Contains(err.Error(), "not exist") {
		t.Errorf("missing file: %v", err)
	}
}

func TestFSCachesBlobs(t *testing.T) {
	f := &fakeGitHub{files: fleet}
	g, err := newFS(t, f, "")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		data, err := fs.ReadFile(g, "clusters/labul/proxmox/orders-xrs/b.yaml")
		if err != nil || string(data) != "kind: B\n" {
			t.Fatalf("read = %q, %v", data, err)
		}
	}
	if n := f.blobCalls.Load(); n != 1 {
		t.Errorf("blob calls = %d, want 1 (cached)", n)
	}
}

func TestFSErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    *fakeGitHub
		ref  string
		want string
	}{
		{"private or missing", &fakeGitHub{status: http.StatusNotFound}, "main", "private repositories answer 404"},
		{"bad token", &fakeGitHub{status: http.StatusUnauthorized}, "main", "token was refused"},
		{"unknown ref", &fakeGitHub{files: fleet}, "nope", "not found"},
		{"truncated", &fakeGitHub{files: fleet, truncated: true}, "main", "truncated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newFS(t, tc.f, tc.ref)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSplitRepo(t *testing.T) {
	for in, ok := range map[string]bool{"a/b": true, "a": false, "/b": false, "a/": false, "a/b/c": false} {
		if _, _, err := ghfs.SplitRepo(in); (err == nil) != ok {
			t.Errorf("SplitRepo(%q) err = %v", in, err)
		}
	}
}

func TestPrefetch(t *testing.T) {
	f := &fakeGitHub{files: fleet}
	g, err := newFS(t, f, "main")
	if err != nil {
		t.Fatal(err)
	}
	all := []string{"README.md", "clusters/labda/vsphere/machinery-xrs/a.yaml", "clusters/labul/proxmox/orders-xrs/b.yaml", "missing.yaml", "clusters"}
	if err := g.Prefetch(all, 2); err != nil {
		t.Fatal(err)
	}
	if n := f.blobCalls.Load(); n != 3 {
		t.Errorf("blob calls = %d, want 3 (missing paths and directories skipped)", n)
	}
	if _, err := fs.ReadFile(g, "README.md"); err != nil || f.blobCalls.Load() != 3 {
		t.Errorf("read after prefetch fetched again: %v, calls %d", err, f.blobCalls.Load())
	}
}
