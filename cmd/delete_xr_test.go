package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

const fxDir = "clusters/labda/vsphere/machinery-xrs"

func fxVM(kind, name, file string) string {
	return "apiVersion: resources.stuttgart-things.com/v1alpha1\nkind: " + kind + "\nmetadata:\n  name: " + name +
		"\n  annotations:\n    stuttgart-things.com/source-path: " + file + "\nspec: {}\n"
}

// xrRepo builds a fleet-repo fixture with two XRs and a kustomization.yaml,
// committed on main, with a bare repository as origin.
func xrFixtureRepo(t *testing.T) (root, remote string) {
	t.Helper()
	root = t.TempDir()
	files := map[string]string{
		fxDir + "/kustomization.yaml": "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - dev7.yaml\n  - dev8.yaml\n",
		fxDir + "/dev7.yaml":          fxVM("NativeVsphereVM", "dev7-vm", fxDir+"/dev7.yaml"),
		fxDir + "/dev8.yaml":          fxVM("NativeVsphereVM", "dev8-vm", fxDir+"/dev8.yaml"),
	}
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := git.PlainInitWithOptions(root, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")},
	})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	if err := wt.AddGlob("clusters"); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "t", Email: "t@example.com", When: time.Now()}
	if _, err := wt.Commit("init", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}
	remote = t.TempDir()
	if _, err := git.PlainInit(remote, true); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{remote}}); err != nil {
		t.Fatal(err)
	}
	return root, remote
}

func treeFiles(t *testing.T, repoPath, ref string) (map[string]string, *object.Commit) {
	t.Helper()
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.Reference(plumbing.ReferenceName(ref), true)
	if err != nil {
		t.Fatalf("%s: %v", ref, err)
	}
	c, err := repo.CommitObject(r.Hash())
	if err != nil {
		t.Fatal(err)
	}
	tree, _ := c.Tree()
	out := map[string]string{}
	_ = tree.Files().ForEach(func(f *object.File) error {
		s, _ := f.Contents()
		out[f.Name] = s
		return nil
	})
	return out, c
}

func TestDeleteXRPushesDeleteClaimBranch(t *testing.T) {
	root, remote := xrFixtureRepo(t)
	t.Chdir(root)
	t.Setenv("GIT_USER", "tester")
	t.Setenv("GIT_TOKEN", "x")

	cfg := &DeleteConfig{
		RegistryPath: "claims/registry.yaml",
		XR:           &DeleteXRConfig{Kind: "NativeVsphereVM", Name: "dev7-vm", Reason: "test"},
		GitConfig:    &GitConfig{Commit: true, Push: true, Remote: "origin"},
	}
	if !cfg.xrMode() {
		t.Fatal("xrMode() = false")
	}
	if err := runDeleteXR(cfg, nil); err != nil {
		t.Fatal(err)
	}

	files, commit := treeFiles(t, remote, "refs/heads/delete-claim/dev7-vm")
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != fxDir+"/dev8.yaml,"+fxDir+"/kustomization.yaml" {
		t.Errorf("pushed tree = %v", names)
	}
	if k := files[fxDir+"/kustomization.yaml"]; strings.Contains(k, "dev7") || !strings.Contains(k, "- dev8.yaml") {
		t.Errorf("kustomization.yaml:\n%s", k)
	}
	if commit.Message != "Delete claim NativeVsphereVM/dev7-vm" {
		t.Errorf("commit message = %q", commit.Message)
	}
	if len(commit.ParentHashes) != 1 {
		t.Errorf("want one parent (main), got %d", len(commit.ParentHashes))
	}
}

func TestDeleteXRRefusesWrongBranchAndBase(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	t.Chdir(root)
	t.Setenv("GIT_USER", "tester")
	t.Setenv("GIT_TOKEN", "x")

	cfg := &DeleteConfig{
		XR:        &DeleteXRConfig{Name: "dev7-vm"},
		GitConfig: &GitConfig{Commit: true, Push: true, Branch: "my-branch"},
	}
	if err := runDeleteXR(cfg, nil); err == nil || !strings.Contains(err.Error(), "delete-claim/") {
		t.Errorf("custom branch: %v", err)
	}

	repo, _ := git.PlainOpen(root)
	head, _ := repo.Head()
	_ = repo.Storer.SetReference(plumbing.NewHashReference("refs/heads/feature", head.Hash()))
	wt, _ := repo.Worktree()
	if err := wt.Checkout(&git.CheckoutOptions{Branch: "refs/heads/feature"}); err != nil {
		t.Fatal(err)
	}
	cfg = &DeleteConfig{XR: &DeleteXRConfig{Name: "dev7-vm"}, GitConfig: &GitConfig{Commit: true, Push: true}}
	if err := runDeleteXR(cfg, nil); err == nil || !strings.Contains(err.Error(), "check out main first") {
		t.Errorf("not on main: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, fxDir, "dev7.yaml")); err != nil {
		t.Error("a refused deletion touched the file")
	}
}

func TestDeleteXRLocalAndDryRun(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	t.Chdir(root)

	cfg := &DeleteConfig{XR: &DeleteXRConfig{SourcePath: fxDir + "/dev8.yaml"}, DryRun: true}
	if err := runDeleteXR(cfg, nil); err != nil {
		t.Fatal(err)
	}
	if cfg.XR.Kind != "NativeVsphereVM" || cfg.XR.Name != "dev8-vm" {
		t.Errorf("resolved from the file: %+v", cfg.XR)
	}
	if _, err := os.Stat(filepath.Join(root, fxDir, "dev8.yaml")); err != nil {
		t.Fatal("dry run removed the file")
	}

	cfg = &DeleteConfig{XR: &DeleteXRConfig{SourcePath: fxDir + "/dev8.yaml"}}
	if err := runDeleteXR(cfg, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, fxDir, "dev8.yaml")); !os.IsNotExist(err) {
		t.Error("local deletion kept the file")
	}
	k, _ := os.ReadFile(filepath.Join(root, fxDir, "kustomization.yaml"))
	if strings.Contains(string(k), "dev8") {
		t.Errorf("kustomization.yaml still lists dev8:\n%s", k)
	}
}

func TestRegistryMissing(t *testing.T) {
	root, _ := xrFixtureRepo(t)
	if !registryMissing(root, "claims/registry.yaml") {
		t.Error("fleet repo without registry: want XR mode")
	}
	_ = os.MkdirAll(filepath.Join(root, "claims"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "claims", "registry.yaml"), []byte("claims: []\n"), 0o644)
	if registryMissing(root, "claims/registry.yaml") {
		t.Error("registry present: want registry mode")
	}
}
