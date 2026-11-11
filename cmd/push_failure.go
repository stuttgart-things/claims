package cmd

import (
	"errors"
	"fmt"

	"github.com/stuttgart-things/claims/internal/gitops"
)

// After the commit, a failed push or PR used to leave a half-done branch
// behind without a word (claims#114). claims does not roll it back: the
// commit holds what was rendered -- generated passwords included -- and a
// reset would lose it, while the cause (a token, the network) is usually
// fixed in a minute. Instead the error says exactly where the work is and
// how to finish or undo it.

// pushFailed wraps a push error with the commands to retry or undo it.
// prevBranch is the branch checked out before claims created branch ("" when
// it committed on the current branch); cloned marks a temporary clone, which
// is removed on return.
func pushFailed(err error, g *gitops.GitOps, remote, branch, prevBranch, base string, cloned bool) error {
	if cloned {
		return fmt.Errorf("%w\nNothing was pushed; the temporary clone is removed. Fix the cause (e.g. %s) and run claims again", err, gitTokenFix)
	}
	repo := g.RepoPath
	undo := fmt.Sprintf("git -C %s switch %s && git -C %s branch -D %s", repo, prevBranch, repo, branch)
	if prevBranch == "" || prevBranch == branch {
		undo = fmt.Sprintf("git -C %s reset --soft HEAD~1   (keeps the files, drops the commit)", repo)
	}
	if base == "" {
		base = "main"
	}
	return fmt.Errorf(`%w
Nothing was pushed. The commit is on the local branch %s in %s.
  retry:  git -C %s push -u %s %s
          then: gh pr create --head %s --base %s   (if a PR was asked for)
  undo:   %s`, err, branch, repo, repo, remote, branch, branch, base, undo)
}

// mergeStepError marks a --merge failure after the PR was opened: prFailed
// passes it through (the PR exists; no `gh pr create` hint).
type mergeStepError struct{ err error }

func (e *mergeStepError) Error() string { return e.err.Error() }
func (e *mergeStepError) Unwrap() error { return e.err }

// mergeStep marks err as a failure of the merge step.
func mergeStep(err error) error {
	if err == nil {
		return nil
	}
	return &mergeStepError{err}
}

// prFailed wraps a PR error after a successful push.
func prFailed(err error, branch, base string) error {
	var me *mergeStepError
	if errors.As(err, &me) {
		return err
	}
	if base == "" {
		base = "main"
	}
	return fmt.Errorf("creating pull request: %w\nThe branch %s is pushed; open the PR with: gh pr create --head %s --base %s", err, branch, branch, base)
}

// currentBranch is the checked-out branch, "" if unknown.
func currentBranch(g *gitops.GitOps) string {
	b, err := g.GetCurrentBranch()
	if err != nil {
		return ""
	}
	return b
}

// prBaseOf is the PR's base branch ("" = main).
func prBaseOf(pr *PRConfig) string {
	if pr == nil {
		return ""
	}
	return pr.BaseBranch
}
