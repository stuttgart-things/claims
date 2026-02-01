# Phase 6: GitOps Integration

## Goal
Add git commit and push functionality for GitOps workflows.

---

## Tasks

- [ ] Create `internal/gitops/operations.go` (go-git)
- [ ] Create `internal/gitops/branch.go` (branch creation)
- [ ] Add all `--git-*` flags
- [ ] Create `cmd/render_git.go`
- [ ] Integrate git into both interactive/non-interactive flows

---

## New Flags

```go
// cmd/render.go - add to init()
var (
    gitCommit       bool
    gitPush         bool
    gitCreateBranch bool
    gitMessage      string
    gitBranch       string
    gitRemote       string
    gitRepo         string
    gitUser         string
    gitToken        string
)

renderCmd.Flags().BoolVarP(&gitCommit, "git-commit", "g", false, "Commit rendered files")
renderCmd.Flags().BoolVar(&gitPush, "git-push", false, "Push to remote (implies --git-commit)")
renderCmd.Flags().BoolVar(&gitCreateBranch, "git-create-branch", false, "Create new branch before committing")
renderCmd.Flags().StringVar(&gitMessage, "git-message", "", "Commit message")
renderCmd.Flags().StringVar(&gitBranch, "git-branch", "", "Target branch (default: current)")
renderCmd.Flags().StringVar(&gitRemote, "git-remote", "origin", "Remote name")
renderCmd.Flags().StringVar(&gitRepo, "git-repo", "", "Clone this repo first (for stateless CI/CD)")
renderCmd.Flags().StringVar(&gitUser, "git-user", "", "Git username (default: $GIT_USER)")
renderCmd.Flags().StringVar(&gitToken, "git-token", "", "Git token (default: $GIT_TOKEN)")
```

---

## Git Operations Package

### `internal/gitops/operations.go`

```go
package gitops

import (
    "fmt"
    "os"
    "path/filepath"
    "time"

    git "github.com/go-git/go-git/v5"
    "github.com/go-git/go-git/v5/config"
    "github.com/go-git/go-git/v5/plumbing"
    "github.com/go-git/go-git/v5/plumbing/object"
    "github.com/go-git/go-git/v5/plumbing/transport/http"
)

type GitOps struct {
    RepoPath string
    repo     *git.Repository
    auth     *http.BasicAuth
}

type Config struct {
    RepoPath     string
    RepoURL      string // For clone-based workflow
    Branch       string
    CreateBranch bool
    Remote       string
    User         string
    Token        string
    CommitMsg    string
}

// New creates a GitOps instance for an existing repo
func New(repoPath string, user, token string) (*GitOps, error) {
    repo, err := git.PlainOpen(repoPath)
    if err != nil {
        return nil, fmt.Errorf("opening repository: %w", err)
    }

    g := &GitOps{
        RepoPath: repoPath,
        repo:     repo,
    }

    if user != "" && token != "" {
        g.auth = &http.BasicAuth{
            Username: user,
            Password: token,
        }
    }

    return g, nil
}

// Clone clones a repository to a temp directory
func Clone(url, user, token string) (*GitOps, string, error) {
    tmpDir, err := os.MkdirTemp("", "claims-gitops-*")
    if err != nil {
        return nil, "", fmt.Errorf("creating temp directory: %w", err)
    }

    auth := &http.BasicAuth{
        Username: user,
        Password: token,
    }

    repo, err := git.PlainClone(tmpDir, false, &git.CloneOptions{
        URL:      url,
        Auth:     auth,
        Progress: os.Stdout,
    })
    if err != nil {
        os.RemoveAll(tmpDir)
        return nil, "", fmt.Errorf("cloning repository: %w", err)
    }

    return &GitOps{
        RepoPath: tmpDir,
        repo:     repo,
        auth:     auth,
    }, tmpDir, nil
}

// AddFiles stages files for commit
func (g *GitOps) AddFiles(files []string) error {
    worktree, err := g.repo.Worktree()
    if err != nil {
        return fmt.Errorf("getting worktree: %w", err)
    }

    for _, f := range files {
        // Convert absolute path to relative
        relPath, err := filepath.Rel(g.RepoPath, f)
        if err != nil {
            relPath = f
        }

        if _, err := worktree.Add(relPath); err != nil {
            return fmt.Errorf("staging %s: %w", relPath, err)
        }
    }

    return nil
}

// Commit creates a commit with the staged changes
func (g *GitOps) Commit(message, authorName, authorEmail string) error {
    worktree, err := g.repo.Worktree()
    if err != nil {
        return fmt.Errorf("getting worktree: %w", err)
    }

    if authorName == "" {
        authorName = "claims-cli"
    }
    if authorEmail == "" {
        authorEmail = "claims-cli@automated"
    }

    _, err = worktree.Commit(message, &git.CommitOptions{
        Author: &object.Signature{
            Name:  authorName,
            Email: authorEmail,
            When:  time.Now(),
        },
    })
    if err != nil {
        return fmt.Errorf("committing: %w", err)
    }

    return nil
}

// Push pushes to remote
func (g *GitOps) Push(remote string) error {
    if g.auth == nil {
        return fmt.Errorf("git credentials required for push")
    }

    err := g.repo.Push(&git.PushOptions{
        RemoteName: remote,
        Auth:       g.auth,
    })
    if err != nil && err != git.NoErrAlreadyUpToDate {
        return fmt.Errorf("pushing: %w", err)
    }

    return nil
}

// Cleanup removes the repository directory (for clone-based workflows)
func (g *GitOps) Cleanup() error {
    return os.RemoveAll(g.RepoPath)
}
```

### `internal/gitops/branch.go`

```go
package gitops

import (
    "fmt"

    git "github.com/go-git/go-git/v5"
    "github.com/go-git/go-git/v5/plumbing"
)

// CreateBranch creates and checks out a new branch
func (g *GitOps) CreateBranch(name string) error {
    worktree, err := g.repo.Worktree()
    if err != nil {
        return fmt.Errorf("getting worktree: %w", err)
    }

    // Get current HEAD
    headRef, err := g.repo.Head()
    if err != nil {
        return fmt.Errorf("getting HEAD: %w", err)
    }

    // Create new branch reference
    branchRef := plumbing.NewBranchReferenceName(name)
    ref := plumbing.NewHashReference(branchRef, headRef.Hash())

    if err := g.repo.Storer.SetReference(ref); err != nil {
        return fmt.Errorf("creating branch: %w", err)
    }

    // Checkout the new branch
    if err := worktree.Checkout(&git.CheckoutOptions{
        Branch: branchRef,
    }); err != nil {
        return fmt.Errorf("checking out branch: %w", err)
    }

    return nil
}

// CheckoutBranch checks out an existing branch
func (g *GitOps) CheckoutBranch(name string) error {
    worktree, err := g.repo.Worktree()
    if err != nil {
        return fmt.Errorf("getting worktree: %w", err)
    }

    branchRef := plumbing.NewBranchReferenceName(name)
    if err := worktree.Checkout(&git.CheckoutOptions{
        Branch: branchRef,
    }); err != nil {
        return fmt.Errorf("checking out branch: %w", err)
    }

    return nil
}

// GetCurrentBranch returns the name of the current branch
func (g *GitOps) GetCurrentBranch() (string, error) {
    head, err := g.repo.Head()
    if err != nil {
        return "", fmt.Errorf("getting HEAD: %w", err)
    }

    return head.Name().Short(), nil
}
```

### `internal/gitops/auth.go`

```go
package gitops

import (
    "fmt"
    "os"
)

// ResolveCredentials gets git credentials from flags or environment
func ResolveCredentials(user, token string) (string, string, error) {
    if user == "" {
        user = os.Getenv("GIT_USER")
    }
    if token == "" {
        token = os.Getenv("GIT_TOKEN")
        if token == "" {
            token = os.Getenv("GITHUB_TOKEN") // GitHub Actions compatibility
        }
    }

    if user == "" || token == "" {
        return "", "", fmt.Errorf("git credentials required: set --git-user/--git-token or GIT_USER/GIT_TOKEN environment variables")
    }

    return user, token, nil
}
```

---

## Git Integration in Render

### `cmd/render_git.go`

```go
package cmd

import (
    "fmt"
    "path/filepath"
    "strings"

    "github.com/stuttgart-things/claims/internal/gitops"
)

func executeGitOperations(results []RenderResult, config *RenderConfig) error {
    if config.GitConfig == nil || (!config.GitConfig.Commit && !config.GitConfig.Push) {
        return nil
    }

    // Resolve credentials if pushing
    user, token := config.GitConfig.User, config.GitConfig.Token
    if config.GitConfig.Push {
        var err error
        user, token, err = gitops.ResolveCredentials(user, token)
        if err != nil {
            return err
        }
    }

    var g *gitops.GitOps
    var tmpDir string
    var err error

    // Clone-based or local workflow
    if config.GitConfig.RepoURL != "" {
        fmt.Printf("Cloning %s...\n", config.GitConfig.RepoURL)
        g, tmpDir, err = gitops.Clone(config.GitConfig.RepoURL, user, token)
        if err != nil {
            return err
        }
        defer g.Cleanup()

        // Adjust output directory to be inside cloned repo
        config.OutputDir = filepath.Join(tmpDir, config.OutputDir)
    } else {
        // Find repo root from output directory
        repoPath, err := findRepoRoot(config.OutputDir)
        if err != nil {
            return fmt.Errorf("output directory is not in a git repository: %w", err)
        }
        g, err = gitops.New(repoPath, user, token)
        if err != nil {
            return err
        }
    }

    // Create branch if requested
    if config.GitConfig.CreateBranch && config.GitConfig.Branch != "" {
        fmt.Printf("Creating branch: %s\n", config.GitConfig.Branch)
        if err := g.CreateBranch(config.GitConfig.Branch); err != nil {
            return err
        }
    } else if config.GitConfig.Branch != "" {
        if err := g.CheckoutBranch(config.GitConfig.Branch); err != nil {
            return err
        }
    }

    // Collect file paths
    var filePaths []string
    for _, r := range results {
        if r.OutputPath != "" && r.Error == nil {
            filePaths = append(filePaths, r.OutputPath)
        }
    }

    // Stage files
    fmt.Println("Staging files...")
    if err := g.AddFiles(filePaths); err != nil {
        return err
    }

    // Generate commit message
    message := config.GitConfig.Message
    if message == "" {
        var names []string
        for _, r := range results {
            if r.Error == nil {
                names = append(names, r.TemplateName)
            }
        }
        message = fmt.Sprintf("Rendered claims: %s", strings.Join(names, ", "))
    }

    // Commit
    fmt.Printf("Committing: %s\n", message)
    if err := g.Commit(message, user, ""); err != nil {
        return err
    }

    // Push if requested
    if config.GitConfig.Push {
        fmt.Printf("Pushing to %s...\n", config.GitConfig.Remote)
        if err := g.Push(config.GitConfig.Remote); err != nil {
            return err
        }
        fmt.Println("✓ Pushed successfully")
    }

    return nil
}

func findRepoRoot(startPath string) (string, error) {
    absPath, err := filepath.Abs(startPath)
    if err != nil {
        return "", err
    }

    for {
        gitDir := filepath.Join(absPath, ".git")
        if _, err := os.Stat(gitDir); err == nil {
            return absPath, nil
        }

        parent := filepath.Dir(absPath)
        if parent == absPath {
            return "", fmt.Errorf("not a git repository")
        }
        absPath = parent
    }
}
```

---

## Interactive Git Form

```go
// cmd/render_interactive.go - add git options form

func runGitOptionsForm() (*GitConfig, error) {
    var (
        gitAction    string // "none", "commit", "push", "pr"
        createBranch bool
        branchName   string
    )

    form := huh.NewForm(
        huh.NewGroup(
            huh.NewSelect[string]().
                Title("Git operations").
                Options(
                    huh.NewOption("None - just save files locally", "none"),
                    huh.NewOption("Commit only", "commit"),
                    huh.NewOption("Commit & Push", "push"),
                    huh.NewOption("Commit, Push & Create PR", "pr"),
                ).
                Value(&gitAction),
        ),
    )

    if err := form.Run(); err != nil {
        return nil, err
    }

    if gitAction == "none" {
        return nil, nil
    }

    // Branch options
    branchForm := huh.NewForm(
        huh.NewGroup(
            huh.NewConfirm().
                Title("Create new branch?").
                Value(&createBranch),
        ),
    )

    if err := branchForm.Run(); err != nil {
        return nil, err
    }

    if createBranch {
        nameForm := huh.NewForm(
            huh.NewGroup(
                huh.NewInput().
                    Title("Branch name").
                    Value(&branchName),
            ),
        )
        if err := nameForm.Run(); err != nil {
            return nil, err
        }
    }

    return &GitConfig{
        Commit:       gitAction != "none",
        Push:         gitAction == "push" || gitAction == "pr",
        CreateBranch: createBranch,
        Branch:       branchName,
        Remote:       "origin",
    }, nil
}
```

---

## Verification

```bash
# Commit only
claims render -o ./manifests --git-commit

# Commit and push
claims render -o ./manifests --git-push

# Create new branch, commit, push
claims render -o ./manifests --git-create-branch --git-branch feature/update --git-push

# Clone-based workflow
claims render --git-repo https://github.com/org/repo.git -o ./manifests --git-push
```

---

## Dependencies to Add

```go
// go.mod
require (
    github.com/go-git/go-git/v5 v5.x.x
)
```

---

## Files to Create

| File | Action |
|------|--------|
| `internal/gitops/operations.go` | Create |
| `internal/gitops/branch.go` | Create |
| `internal/gitops/auth.go` | Create |
| `cmd/render_git.go` | Create |
| `cmd/render.go` | Add flags |
| `cmd/render_interactive.go` | Add git form |
