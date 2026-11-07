package gitops

import (
	"context"
	"fmt"

	"github.com/google/go-github/v93/github"

	"github.com/stuttgart-things/claims/internal/ghclient"
)

// PRConfig holds pull request configuration
type PRConfig struct {
	Title       string
	Description string
	Labels      []string
	BaseBranch  string
	HeadBranch  string // "" = the checkout's current branch
	Token       string // "" = ghclient.Token's order (GIT_TOKEN, GITHUB_TOKEN, gh)
}

// PRResult holds the result of PR creation
type PRResult struct {
	Number int
	URL    string
}

// prTarget is the client and the GitHub repository of the checkout at
// repoPath (its origin remote).
func prTarget(repoPath, token string) (*github.Client, string, string, error) {
	g, err := New(repoPath, "", "")
	if err != nil {
		return nil, "", "", err
	}
	url, err := g.GetRemoteURL("origin")
	if err != nil {
		return nil, "", "", fmt.Errorf("finding the GitHub repository: %w", err)
	}
	ownerRepo, err := ghclient.RepoFromURL(url)
	if err != nil {
		return nil, "", "", err
	}
	owner, repo, _ := ghclient.SplitRepo(ownerRepo)
	client, err := ghclient.FromEnv(token)
	if err != nil {
		return nil, "", "", err
	}
	return client, owner, repo, nil
}

// CreatePR opens a pull request in the GitHub repository of the checkout at
// repoPath (its origin remote) through the GitHub API, then adds the labels.
// When only the labels fail, the PR is returned together with the error.
func CreatePR(config PRConfig, repoPath string) (*PRResult, error) {
	client, owner, repo, err := prTarget(repoPath, config.Token)
	if err != nil {
		return nil, err
	}
	head := config.HeadBranch
	if head == "" {
		g, err := New(repoPath, "", "")
		if err != nil {
			return nil, err
		}
		if head, err = g.GetCurrentBranch(); err != nil {
			return nil, fmt.Errorf("finding the branch to open the PR from: %w", err)
		}
	}
	ctx := context.Background()
	pr, _, err := client.PullRequests.Create(ctx, owner, repo, github.CreatePullRequest{
		Title: new(config.Title),
		Body:  new(config.Description),
		Base:  config.BaseBranch,
		Head:  head,
	})
	if err != nil {
		return nil, ghclient.Describe(err, fmt.Sprintf("creating the pull request %s -> %s in %s/%s", head, config.BaseBranch, owner, repo))
	}
	res := &PRResult{Number: pr.GetNumber(), URL: pr.GetHTMLURL()}
	if err := addLabels(ctx, client, owner, repo, res.Number, config.Labels); err != nil {
		return res, fmt.Errorf("created %s, but %w", res.URL, err)
	}
	return res, nil
}

// AddLabelsToPR adds labels to an existing PR of the checkout's repository.
func AddLabelsToPR(prNumber int, labels []string, repoPath string) error {
	if len(nonEmpty(labels)) == 0 {
		return nil
	}
	client, owner, repo, err := prTarget(repoPath, "")
	if err != nil {
		return err
	}
	return addLabels(context.Background(), client, owner, repo, prNumber, labels)
}

func addLabels(ctx context.Context, client *github.Client, owner, repo string, number int, labels []string) error {
	labels = nonEmpty(labels)
	if len(labels) == 0 {
		return nil
	}
	if _, _, err := client.Issues.AddLabelsToIssue(ctx, owner, repo, number, labels); err != nil {
		return ghclient.Describe(err, fmt.Sprintf("adding labels %v", labels))
	}
	return nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// CheckGitHubAuth fails early, before a push, when there is no token to open
// the PR with.
func CheckGitHubAuth(token string) error {
	_, err := ghclient.Token(token)
	return err
}
