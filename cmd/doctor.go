package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// claims doctor (claims#114): the preflight checks on their own, read-only.

var doctorFlags struct {
	apiURL   string
	dir      string
	remote   string
	gitUser  string
	gitToken string
	noAPI    bool
	noGit    bool
	noGitHub bool
}

var doctorCmd = &cobra.Command{
	Use:     "doctor",
	GroupID: groupAuth,
	Short:   "Check API endpoint, login and git/GitHub credentials (changes nothing)",
	Long: `Runs the checks render, encrypt and delete run before they start, and
prints every result:

  API endpoint   which API (-a, $CLAIM_API_URL, --endpoint/$CLAIM_ENDPOINT, the current endpoint)
  API reachable  GET /api/v1/claim-templates answers like a claim-machinery-api
  API auth       the token claims would send (CLAIM_API_TOKEN, OIDC client credentials,
                 or the cached ` + "`claims login`" + `) is valid, or not needed
  git repo       --dir is a checkout (default: the current directory)
  git token      --git-token, GIT_TOKEN, GITHUB_TOKEN or ` + "`gh auth token`" + `
  git remote     the remote accepts the token (git ls-remote; SSH remotes over HTTPS)
  GitHub PR      the token may push to the repository and open pull requests

Only reads: no file, branch or commit is written. Exits 1 when a check fails.`,
	Example: `  claims doctor
  claims doctor --endpoint labul
  claims doctor --dir ~/projects/stuttgart-things --no-api`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runDoctor(cmd.Context(), os.Stdout))
	},
}

func init() {
	f := doctorCmd.Flags()
	f.StringVarP(&doctorFlags.apiURL, "api-url", "a", "", "API URL (default: $CLAIM_API_URL, --endpoint, the current endpoint)")
	f.StringVar(&doctorFlags.dir, "dir", ".", "Checkout of the fleet repository to check")
	f.StringVar(&doctorFlags.remote, "git-remote", "origin", "Git remote name")
	f.StringVar(&doctorFlags.gitUser, "git-user", "", "Git username (or GIT_USER/GITHUB_USER env)")
	f.StringVar(&doctorFlags.gitToken, "git-token", "", "Git token (or GIT_TOKEN/GITHUB_TOKEN env, or gh auth token)")
	f.BoolVar(&doctorFlags.noAPI, "no-api", false, "Skip the API checks")
	f.BoolVar(&doctorFlags.noGit, "no-git", false, "Skip the git and GitHub checks")
	f.BoolVar(&doctorFlags.noGitHub, "no-github", false, "Skip the GitHub PR check")
	addEndpointFlag(doctorCmd)
	addAPIAuthFlags(doctorCmd)
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(ctx context.Context, out io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r := &preflightReport{}
	if !doctorFlags.noAPI {
		// Never prompts: doctor reports what a non-interactive run would use.
		t, err := resolveAPITarget(doctorFlags.apiURL, false)
		if checkAPITarget(r, t, err) {
			if auth, err := resolveAPIAuth(); err != nil {
				r.fail("API auth", err.Error(), "check --api-token / CLAIM_API_OIDC_*")
			} else {
				for _, u := range splitAPIURLs(t.URL) {
					checkAPI(ctx, r, t, u, auth)
				}
			}
		}
	}
	if !doctorFlags.noGit {
		root, err := findRepoRoot(doctorFlags.dir)
		if err != nil {
			r.warn("git repo", fmt.Sprintf("%s is not in a git checkout: git checks skipped (use --dir)", doctorFlags.dir))
		} else {
			checkGit(ctx, r, gitPreflight{
				RepoRoot: root,
				Remote:   doctorFlags.remote,
				Push:     true,
				PR:       !doctorFlags.noGitHub,
				User:     doctorFlags.gitUser,
				Token:    doctorFlags.gitToken,
			})
		}
	}
	r.Print(out, true)
	if n := r.Problems(); n > 0 {
		return fmt.Errorf("%d check(s) failed", n)
	}
	_, _ = fmt.Fprintln(out, "\nAll checks passed.")
	return nil
}
