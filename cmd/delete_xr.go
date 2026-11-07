package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/stuttgart-things/claims/internal/gitops"
	"github.com/stuttgart-things/claims/internal/xrdelete"
)

// XR deletion (claims#93): a claim in an *-xrs directory of the fleet
// repository, named by kind/name or by its source-path. The CLI does the file
// half and opens delete-claim/<name>; the existing pipeline in
// stuttgart-things/stuttgart-things gates it (pr-claim-delete.yaml) and,
// after the merge, deletes the object (delete-claim-xr.yaml). The CLI never
// deletes anything on a cluster.

// DeleteXRConfig holds the XR-mode settings of `claims delete`.
type DeleteXRConfig struct {
	Kind        string
	Name        string
	Namespace   string
	SourcePath  string
	Reason      string
	RequestedBy string
}

// xrMode reports whether the delete flags ask for an XR deletion.
func (c *DeleteConfig) xrMode() bool {
	return c.XR != nil && (c.XR.Kind != "" || c.XR.Name != "" || c.XR.SourcePath != "")
}

// registryMissing reports whether repoRoot has no claims registry but *-xrs
// directories, i.e. an interactive delete should offer XRs.
func registryMissing(repoRoot, registryPath string) bool {
	if _, err := os.Stat(filepath.Join(repoRoot, registryPath)); err == nil {
		return false
	}
	dirs, err := xrdelete.XRSDirs(repoRoot)
	return err == nil && len(dirs) > 0
}

// resolveXR fills kind/name/namespace/source-path from what was given:
// kind+name (searched in the *-xrs directories) or source-path (read from
// the file).
func resolveXR(repoRoot string, x *DeleteXRConfig) error {
	return resolveXRFS(os.DirFS(repoRoot), x)
}

// resolveXRFS is resolveXR over a repository tree (checkout or GitHub ref).
func resolveXRFS(fsys fs.FS, x *DeleteXRConfig) error {
	if x.Namespace == "" {
		x.Namespace = "default"
	}
	if x.SourcePath == "" {
		if x.Name == "" {
			return errors.New("--name (with --kind) or --source-path is required")
		}
		found, err := xrdelete.FindFS(fsys, x.Kind, x.Name, x.Namespace)
		if err != nil {
			return err
		}
		x.Kind, x.SourcePath = found.Kind, found.File
		return nil
	}
	if err := xrdelete.CheckPath(x.SourcePath); err != nil {
		return err
	}
	if x.Kind != "" && x.Name != "" {
		return nil
	}
	data, err := fs.ReadFile(fsys, x.SourcePath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", x.SourcePath, err)
	}
	xrs, err := xrdelete.ParseXRs(data, x.SourcePath)
	if err != nil {
		return err
	}
	if len(xrs) != 1 {
		return fmt.Errorf("%s holds %d XRs; only a file with exactly one claim is deleted whole -- split it first", x.SourcePath, len(xrs))
	}
	if x.Kind == "" {
		x.Kind = xrs[0].Kind
	}
	if x.Name == "" {
		x.Name = xrs[0].Name
		x.Namespace = xrs[0].Namespace
	}
	return nil
}

// runDeleteXR deletes one XR claim. pick, when set, chooses the claim
// interactively and may fill reason and the git choice.
func runDeleteXR(config *DeleteConfig, pick func(repoRoot string, config *DeleteConfig) (bool, error)) error {
	if config.GitConfig != nil && config.GitConfig.Branch != "" && !strings.HasPrefix(config.GitConfig.Branch, "delete-claim/") {
		return fmt.Errorf("--git-branch %q: an XR deletion goes on delete-claim/<name>, the only branches the deletion gate and teardown act on", config.GitConfig.Branch)
	}

	repoRoot := ""
	var g *gitops.GitOps
	var user, token string
	if config.RepoURL != "" {
		if config.GitConfig == nil {
			return errors.New("--git-repo-url needs --create-pr (a clone is only useful to push a branch)")
		}
		var err error
		user, token, err = gitops.ResolveCredentials(config.GitConfig.User, config.GitConfig.Token)
		if err != nil {
			return err
		}
		fmt.Printf("Cloning %s...\n", config.RepoURL)
		var tmp string
		g, tmp, err = gitops.Clone(config.RepoURL, user, token)
		if err != nil {
			return err
		}
		defer func() { _ = g.Cleanup() }()
		repoRoot = tmp
	} else {
		var err error
		if repoRoot, err = resolveRepoRoot(config); err != nil {
			return err
		}
	}

	if pick != nil {
		ok, err := pick(repoRoot, config)
		if err != nil || !ok {
			return err
		}
	}

	x := config.XR
	if err := resolveXR(repoRoot, x); err != nil {
		return err
	}
	r, err := xrdelete.Plan(repoRoot, x.Kind, x.Name, x.Namespace, x.SourcePath)
	if err != nil {
		return err
	}
	conv := xrdelete.ConventionFor(r, x.RequestedBy, x.Reason)
	printXRRemoval(r, conv, config.DryRun)
	if config.DryRun {
		return nil
	}

	git := config.GitConfig != nil
	if !git {
		if err := xrdelete.Apply(repoRoot, r, true); err != nil {
			return err
		}
		fmt.Println(successStyle.Render("Removed locally. Nothing is deleted on a cluster until a delete-claim/" + x.Name + " pull request is merged (use --create-pr)."))
		return nil
	}

	if g == nil {
		if user == "" {
			user, token, err = gitops.ResolveCredentials(config.GitConfig.User, config.GitConfig.Token)
			if err != nil {
				return err
			}
		}
		if g, err = gitops.New(repoRoot, user, token); err != nil {
			return err
		}
		base := "main"
		if config.PRConfig != nil && config.PRConfig.BaseBranch != "" {
			base = config.PRConfig.BaseBranch
		}
		cur, err := g.GetCurrentBranch()
		if err != nil {
			return err
		}
		if cur != base {
			return fmt.Errorf("check out %s first (on %s): the deletion gate accepts only the claim's removal on top of %s", base, cur, base)
		}
	}
	return pushXRDeletion(g, repoRoot, r, conv, config, user)
}

func pushXRDeletion(g *gitops.GitOps, repoRoot string, r *xrdelete.Removal, conv xrdelete.Convention, config *DeleteConfig, user string) error {
	fmt.Printf("Creating branch: %s\n", conv.Branch)
	if err := g.CreateBranch(conv.Branch); err != nil {
		return err
	}
	if err := xrdelete.Apply(repoRoot, r, false); err != nil {
		return err
	}
	var added, removed []string
	if r.Kustomization != "" {
		added = append(added, filepath.Join(repoRoot, filepath.FromSlash(r.Kustomization)))
	}
	for _, f := range r.Files() {
		removed = append(removed, filepath.Join(repoRoot, filepath.FromSlash(f)))
	}
	fmt.Println("Staging the removal...")
	if err := g.AddWithRemoved(added, removed); err != nil {
		return err
	}
	message := conv.Commit
	if config.GitConfig.Message != "" {
		message = config.GitConfig.Message
	}
	fmt.Printf("Committing: %s\n", message)
	if err := g.Commit(message, user, ""); err != nil {
		return err
	}
	remote := config.GitConfig.Remote
	if remote == "" {
		remote = "origin"
	}
	fmt.Printf("Pushing %s to %s...\n", conv.Branch, remote)
	if err := g.Push(remote, conv.Branch); err != nil {
		return err
	}
	fmt.Println(successStyle.Render("Pushed successfully"))

	if config.PRConfig == nil || !config.PRConfig.Create {
		return nil
	}
	if err := gitops.CheckGitHubAuth(config.GitConfig.token()); err != nil {
		return err
	}
	pr := gitops.PRConfig{
		Title:       conv.Title,
		Description: conv.Body,
		Labels:      append([]string{conv.Label}, config.PRConfig.Labels...),
		BaseBranch:  config.PRConfig.BaseBranch,
		HeadBranch:  conv.Branch,
		Token:       config.GitConfig.token(),
	}
	if config.PRConfig.Description != "" {
		pr.Description = config.PRConfig.Description + "\n\n" + conv.Body
	}
	if pr.BaseBranch == "" {
		pr.BaseBranch = "main"
	}
	fmt.Println("Creating pull request...")
	res, err := gitops.CreatePR(pr, g.RepoPath)
	if err != nil {
		return fmt.Errorf("creating pull request: %w", err)
	}
	fmt.Println(successStyle.Render(fmt.Sprintf("Created PR: %s", res.URL)))
	fmt.Println("pr-claim-delete.yaml checks it; after the merge delete-claim-xr.yaml deletes the object.")
	return nil
}

func printXRRemoval(r *xrdelete.Removal, conv xrdelete.Convention, dryRun bool) {
	if dryRun {
		fmt.Println("\n=== DRY RUN - No changes made ===")
	}
	c := r.Claim
	fmt.Printf("Claim:         %s %s/%s (%s)\n", c.Kind, c.Namespace, c.Name, c.APIVersion)
	fmt.Printf("File:          %s\n", c.File)
	for _, run := range r.Runs {
		fmt.Printf("Run (with it): %s\n", run)
	}
	if r.Kustomization != "" {
		fmt.Printf("Kustomization: %s (remove %s)\n", r.Kustomization, strings.Join(r.RemovedEntries, ", "))
	} else {
		fmt.Println("Kustomization: none (orders directory, Flux applies every file)")
	}
	fmt.Printf("Branch:        %s\n", conv.Branch)
	fmt.Printf("PR title:      %s\n\n", conv.Title)
}

// pickXRInteractive lets the user choose a claim from the *-xrs directories,
// asks for a reason and whether to open the pull request.
func pickXRInteractive(repoRoot string, config *DeleteConfig) (bool, error) {
	if config.XR == nil {
		config.XR = &DeleteXRConfig{}
	}
	if config.XR.SourcePath == "" && config.XR.Name == "" {
		claims, err := xrdelete.Scan(repoRoot)
		if err != nil {
			return false, err
		}
		if len(claims) == 0 {
			fmt.Println("No deletable claims in the *-xrs directories.")
			return false, nil
		}
		var options []huh.Option[string]
		for _, c := range claims {
			label := fmt.Sprintf("%-20s %s/%s  (%s)", c.Kind, c.Namespace, c.Name, c.File)
			options = append(options, huh.NewOption(label, c.File))
		}
		var selected string
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select the claim to delete").
				Description("Claims in the *-xrs directories (one XR per file)").
				Options(options...).
				Height(15).
				Value(&selected),
		)).Run(); err != nil {
			return false, fmt.Errorf("selection form: %w", err)
		}
		config.XR.SourcePath = selected
	}

	var confirm bool
	reason := config.XR.Reason
	if err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Reason (optional)").Value(&reason),
		huh.NewConfirm().
			Title(fmt.Sprintf("Delete the claim in %s?", config.XR.SourcePath)).
			Description("File first: the file and its kustomization entry go in a delete-claim/<name> PR;\nthe object is deleted only after that PR is merged.").
			Affirmative("Yes, delete").
			Negative("Cancel").
			Value(&confirm),
	)).Run(); err != nil {
		return false, fmt.Errorf("confirmation form: %w", err)
	}
	if !confirm {
		fmt.Println("Cancelled.")
		return false, nil
	}
	config.XR.Reason = reason

	if config.GitConfig == nil && !config.DryRun {
		dest, err := runDeleteDestinationChoice()
		if err != nil {
			return false, err
		}
		if dest.useGit {
			config.GitConfig = &GitConfig{Commit: true, Push: true, Remote: "origin"}
			config.PRConfig = &PRConfig{Create: true, BaseBranch: "main"}
		}
	}
	return true, nil
}
