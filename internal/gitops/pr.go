package gitops

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// PRConfig holds pull request configuration
type PRConfig struct {
	Title       string
	Description string
	Labels      []string
	BaseBranch  string
	HeadBranch  string
}

// PRResult holds the result of PR creation
type PRResult struct {
	Number int
	URL    string
}

// CreatePR creates a pull request using gh CLI
func CreatePR(config PRConfig, repoPath string) (*PRResult, error) {
	// Check if gh is available
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh CLI not found: install from https://cli.github.com")
	}

	args := []string{"pr", "create",
		"--title", config.Title,
		"--body", config.Description,
		"--base", config.BaseBranch,
	}

	// Add labels
	for _, label := range config.Labels {
		if label != "" {
			args = append(args, "--label", label)
		}
	}

	// Add head branch if specified
	if config.HeadBranch != "" {
		args = append(args, "--head", config.HeadBranch)
	}

	cmd := exec.Command("gh", args...)
	cmd.Dir = repoPath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("gh pr create failed: %s", errMsg)
	}

	// Parse PR URL from output
	prURL := strings.TrimSpace(stdout.String())

	return &PRResult{
		URL: prURL,
	}, nil
}

// AddLabelsToPR adds labels to an existing PR
func AddLabelsToPR(prNumber int, labels []string, repoPath string) error {
	if len(labels) == 0 {
		return nil
	}

	args := []string{"pr", "edit", fmt.Sprintf("%d", prNumber)}
	for _, label := range labels {
		if label != "" {
			args = append(args, "--add-label", label)
		}
	}

	cmd := exec.Command("gh", args...)
	cmd.Dir = repoPath

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("adding labels failed: %s", stderr.String())
	}

	return nil
}

// CheckGHAuth verifies gh CLI is authenticated
func CheckGHAuth() error {
	cmd := exec.Command("gh", "auth", "status")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh CLI not authenticated: run 'gh auth login'")
	}
	return nil
}

// CheckGHInstalled checks if gh CLI is installed
func CheckGHInstalled() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}
