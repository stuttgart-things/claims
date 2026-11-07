// Package ghfs is a read-only fs.FS over one ref of a GitHub repository, so
// claims can read the fleet repository without a local checkout (claims#107).
//
// New lists the whole tree in one call (git trees API, recursive); a file's
// content is fetched on first read (git blobs API) and cached. Paths are the
// repository's, slash-separated, without a leading slash -- the same names
// os.DirFS(checkout) uses, so callers work on either.
package ghfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"sync"
	"time"

	"github.com/google/go-github/v93/github"

	"github.com/stuttgart-things/claims/internal/ghclient"
)

// FS is one ref of a GitHub repository. It is safe for concurrent use.
type FS struct {
	ctx         context.Context
	client      *github.Client
	owner, repo string
	ref         string

	files map[string]fileInfo      // path -> blob
	dirs  map[string][]fs.DirEntry // dir ("." for the root) -> sorted entries

	mu    sync.Mutex
	blobs map[string][]byte // sha -> content
}

type fileInfo struct {
	name string
	sha  string
	size int64
	dir  bool
}

func (f fileInfo) Name() string { return f.name }
func (f fileInfo) Size() int64  { return f.size }
func (f fileInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (f fileInfo) ModTime() time.Time         { return time.Time{} }
func (f fileInfo) IsDir() bool                { return f.dir }
func (f fileInfo) Sys() any                   { return nil }
func (f fileInfo) Type() fs.FileMode          { return f.Mode().Type() }
func (f fileInfo) Info() (fs.FileInfo, error) { return f, nil }

// Repo is "owner/name".
func (f *FS) Repo() string { return f.owner + "/" + f.repo }

// Ref is the branch, tag or commit the tree was read at.
func (f *FS) Ref() string { return f.ref }

// SplitRepo splits "owner/name".
func SplitRepo(ownerRepo string) (owner, repo string, err error) {
	return ghclient.SplitRepo(ownerRepo)
}

// New reads the tree of ownerRepo at ref (a branch, tag or commit).
func New(ctx context.Context, client *github.Client, ownerRepo, ref string) (*FS, error) {
	owner, repo, err := SplitRepo(ownerRepo)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref = "main"
	}
	tree, _, err := client.Git.GetTree(ctx, owner, repo, ref, true)
	if err != nil {
		return nil, describe(err, ownerRepo, ref)
	}
	if tree.GetTruncated() {
		// The trees API caps a recursive listing (100,000 entries / 7 MB);
		// reading directory by directory would be the way out, not needed
		// for the fleet repository today.
		return nil, fmt.Errorf("%s@%s: the GitHub tree listing is truncated (repository too large to read remotely)", ownerRepo, ref)
	}
	f := &FS{
		ctx: ctx, client: client, owner: owner, repo: repo, ref: ref,
		files: map[string]fileInfo{},
		dirs:  map[string][]fs.DirEntry{".": nil},
		blobs: map[string][]byte{},
	}
	for _, e := range tree.Entries {
		p := e.GetPath()
		var info fileInfo
		switch e.GetType() {
		case "blob":
			info = fileInfo{name: path.Base(p), sha: e.GetSHA(), size: int64(e.GetSize())}
			f.files[p] = info
		case "tree":
			info = fileInfo{name: path.Base(p), dir: true}
			if _, ok := f.dirs[p]; !ok {
				f.dirs[p] = nil
			}
		default: // submodules ("commit") are left out
			continue
		}
		parent := path.Dir(p)
		f.dirs[parent] = append(f.dirs[parent], info)
	}
	for d := range f.dirs {
		sort.Slice(f.dirs[d], func(i, j int) bool { return f.dirs[d][i].Name() < f.dirs[d][j].Name() })
	}
	return f, nil
}

// describe: see ghclient.Describe; the trees API answers an unknown ref
// with 422, which is a "not found" too.
func describe(err error, ownerRepo, ref string) error {
	action := fmt.Sprintf("reading %s@%s from GitHub", ownerRepo, ref)
	var gerr *github.ErrorResponse
	if errors.As(err, &gerr) && gerr.Response != nil && gerr.Response.StatusCode == http.StatusUnprocessableEntity {
		gerr.Response.StatusCode = http.StatusNotFound
	}
	return ghclient.Describe(err, action)
}

func (f *FS) lookup(op, name string) (fileInfo, error) {
	if !fs.ValidPath(name) {
		return fileInfo{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	if fi, ok := f.files[name]; ok {
		return fi, nil
	}
	if _, ok := f.dirs[name]; ok {
		return fileInfo{name: path.Base(name), dir: true}, nil
	}
	return fileInfo{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
}

// Stat implements fs.StatFS.
func (f *FS) Stat(name string) (fs.FileInfo, error) { return f.lookup("stat", name) }

// ReadDir implements fs.ReadDirFS.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	fi, err := f.lookup("readdir", name)
	if err != nil {
		return nil, err
	}
	if !fi.dir {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: errors.New("not a directory")}
	}
	return append([]fs.DirEntry(nil), f.dirs[name]...), nil
}

// ReadFile implements fs.ReadFileFS.
func (f *FS) ReadFile(name string) ([]byte, error) {
	fi, err := f.lookup("open", name)
	if err != nil {
		return nil, err
	}
	if fi.dir {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errors.New("is a directory")}
	}
	data, err := f.blob(fi.sha)
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: err}
	}
	return append([]byte(nil), data...), nil
}

func (f *FS) blob(sha string) ([]byte, error) {
	f.mu.Lock()
	data, ok := f.blobs[sha]
	f.mu.Unlock()
	if ok {
		return data, nil
	}
	data, _, err := f.client.Git.GetBlobRaw(f.ctx, f.owner, f.repo, sha)
	if err != nil {
		return nil, describe(err, f.Repo(), f.ref)
	}
	f.mu.Lock()
	f.blobs[sha] = data
	f.mu.Unlock()
	return data, nil
}

// Prefetch reads the given files' contents concurrently (at most parallel
// requests at a time) into the cache, so a following scan does not fetch them
// one by one. Unknown paths and directories are skipped; the first fetch
// error is returned.
func (f *FS) Prefetch(names []string, parallel int) error {
	if parallel < 1 {
		parallel = 1
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	for _, name := range names {
		fi, ok := f.files[name]
		if !ok {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if _, err := f.blob(fi.sha); err != nil {
				once.Do(func() { first = &fs.PathError{Op: "read", Path: name, Err: err} })
			}
		}()
	}
	wg.Wait()
	return first
}

// Open implements fs.FS.
func (f *FS) Open(name string) (fs.File, error) {
	fi, err := f.lookup("open", name)
	if err != nil {
		return nil, err
	}
	if fi.dir {
		return &dirFile{info: fi, entries: f.dirs[name]}, nil
	}
	data, err := f.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &file{info: fi, r: bytes.NewReader(data)}, nil
}

type file struct {
	info fileInfo
	r    *bytes.Reader
}

func (f *file) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *file) Read(b []byte) (int, error) { return f.r.Read(b) }
func (f *file) Close() error               { return nil }

type dirFile struct {
	info    fileInfo
	entries []fs.DirEntry
	off     int
}

func (d *dirFile) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *dirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.name, Err: errors.New("is a directory")}
}
func (d *dirFile) Close() error { return nil }

// ReadDir implements fs.ReadDirFile.
func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.off:]
	if n <= 0 {
		d.off = len(d.entries)
		return append([]fs.DirEntry(nil), rest...), nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if n > len(rest) {
		n = len(rest)
	}
	d.off += n
	return append([]fs.DirEntry(nil), rest[:n]...), nil
}

var (
	_ fs.ReadDirFS  = (*FS)(nil)
	_ fs.ReadFileFS = (*FS)(nil)
	_ fs.StatFS     = (*FS)(nil)
)
