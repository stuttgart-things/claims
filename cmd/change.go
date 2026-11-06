package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/gitops"
	"github.com/stuttgart-things/claims/internal/xrdelete"
)

// claims change (claims#94): resize a VM claim in an *-xrs directory.
//
// A thin wrapper around the fleet repository's dispatch-change-claim.yaml,
// the workflow behind Backstage's request-claim-change: the rules (create-vm's
// size table, a disk never shrinks, which fields may change, the one-shot
// resize run) stay in hack/claim-change.py. claims resolves the claim, starts
// the workflow with the same inputs, waits for it and prints the pull request
// change-claim/<name>, which pr-claim-change.yaml then checks.

const changeWorkflow = "dispatch-change-claim.yaml"

// changeVMKinds are the kinds claim-change.py resizes.
var changeVMKinds = map[string]bool{"NativeProxmoxVM": true, "NativeVsphereVM": true}

// ChangeConfig holds the inputs of a claim change.
type ChangeConfig struct {
	Kind        string
	Name        string
	Namespace   string
	SourcePath  string
	Size        string
	Reboot      string // now, evening, at, none -- or a time, which means at
	RebootAt    string
	RequestedBy string
	Reason      string

	Repo   string // owner/name of the fleet repository ("" = the checkout's)
	Ref    string // branch the workflow runs on
	DryRun bool
	NoWait bool
}

var changeFlags ChangeConfig
var changeInteractive bool

var changeCmd = &cobra.Command{
	Use:     "change",
	Aliases: []string{"resize"},
	GroupID: groupClaims,
	Short:   "Change a claim (VM size) via Git PR",
	Long: `Resize a VM claim (NativeVsphereVM, NativeProxmoxVM) in an *-xrs directory of
the fleet repository.

claims starts the repository's dispatch-change-claim.yaml with the same inputs
as Backstage's request-claim-change, waits for it and prints the pull request
change-claim/<name>. What may change is decided there (hack/claim-change.py):
cpu, memory and disk from create-vm's size table, and the disk never shrinks.
pr-claim-change.yaml checks the PR; merging it applies the new size.

--reboot decides when the one-shot run applies the resize in the guest:
now, evening (19-22 Europe/Berlin, default), none, or a time (ISO-8601, UTC
or Europe/Berlin without offset).

Needs the gh CLI, logged in with permission to run workflows.`,
	Example: `  claims change --kind NativeVsphereVM --name dev7-vm --size xlarge --reboot evening
  claims change --source-path clusters/labda/vsphere/machinery-orders-xrs/dev7.yaml --size xlarge --reboot 2026-11-05T20:00
  claims change --name dev7-vm --size xlarge --dry-run`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := changeFlags
		if cfg.RequestedBy == "" {
			cfg.RequestedBy, _ = gitops.ResolveCredentialsOptional("", "")
		}
		if cfg.RequestedBy == "" && !cfg.DryRun {
			if out, err := gh("api", "user", "--jq", ".login"); err == nil {
				cfg.RequestedBy = strings.TrimSpace(string(out))
			}
		}
		interactive := changeInteractive ||
			(cfg.Name == "" && cfg.SourcePath == "" && (isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())))
		exitOn(runChange(&cfg, interactive))
	},
}

func init() {
	f := changeCmd.Flags()
	f.StringVar(&changeFlags.Kind, "kind", "", "XR kind (NativeVsphereVM, NativeProxmoxVM); optional when the name is unique")
	f.StringVar(&changeFlags.Name, "name", "", "XR name (metadata.name)")
	f.StringVar(&changeFlags.Namespace, "namespace", "default", "XR namespace")
	f.StringVar(&changeFlags.SourcePath, "source-path", "", "File the XR lives in (its stuttgart-things.com/source-path)")
	f.StringVar(&changeFlags.Size, "size", "", "New size (create-vm's: small, medium, large, xlarge)")
	f.StringVar(&changeFlags.Reboot, "reboot", "evening", "When to apply the resize in the guest: now, evening, none, or an ISO-8601 time")
	f.StringVar(&changeFlags.RequestedBy, "requested-by", "", "Who asks for the change (PR body; default: the git user)")
	f.StringVar(&changeFlags.Reason, "reason", "", "Why the claim changes (PR body)")
	f.StringVar(&changeFlags.Repo, "repo", "", "Fleet repository owner/name (default: the current checkout's GitHub repository)")
	f.StringVar(&changeFlags.Ref, "ref", "main", "Branch the workflow runs on")
	f.BoolVar(&changeFlags.DryRun, "dry-run", false, "Print the workflow inputs without starting it")
	f.BoolVar(&changeFlags.NoWait, "no-wait", false, "Start the workflow and return without waiting for the PR")
	f.BoolVarP(&changeInteractive, "interactive", "i", false, "Pick the VM, size and reboot interactively")
	rootCmd.AddCommand(changeCmd)
}

// gh runs the GitHub CLI; tests replace it.
var gh = func(args ...string) ([]byte, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, errors.New("gh CLI not found: install from https://cli.github.com")
	}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("gh %s: %s", args[0]+" "+args[1], strings.TrimSpace(string(ee.Stderr)))
		}
		return out, fmt.Errorf("gh %s: %w", args[0]+" "+args[1], err)
	}
	return out, nil
}

// changePoll is how often the workflow run and the PR are looked up.
var changePoll = 10 * time.Second

func runChange(cfg *ChangeConfig, interactive bool) error {
	if interactive {
		ok, err := pickChangeInteractive(cfg)
		if err != nil || !ok {
			return err
		}
	}
	if err := resolveChange(cfg); err != nil {
		return err
	}
	inputs := changeInputs(cfg)
	printChange(cfg, inputs)
	if cfg.DryRun {
		fmt.Println("\n=== DRY RUN - workflow not started ===")
		return nil
	}

	started := time.Now().Add(-5 * time.Second)
	args := append([]string{"workflow", "run", changeWorkflow, "--ref", cfg.Ref}, repoArgs(cfg)...)
	for _, kv := range inputs {
		args = append(args, "-f", kv[0]+"="+kv[1])
	}
	if _, err := gh(args...); err != nil {
		return err
	}
	fmt.Printf("Started %s on %s.\n", changeWorkflow, cfg.Ref)
	if cfg.NoWait {
		fmt.Printf("The PR will be change-claim/%s.\n", cfg.Name)
		return nil
	}

	runID, err := findDispatchedRun(cfg, started)
	if err != nil {
		return err
	}
	fmt.Printf("Waiting for run %d ...\n", runID)
	conclusion, err := waitRun(cfg, runID)
	if err != nil {
		return err
	}
	if conclusion != "success" {
		hint := ""
		if out, err := gh(append([]string{"run", "view", fmt.Sprint(runID), "--log-failed"}, repoArgs(cfg)...)...); err == nil {
			hint = "\n" + refusalLines(string(out))
		}
		return fmt.Errorf("%s run %d: %s%s", changeWorkflow, runID, conclusion, hint)
	}
	url, err := changePR(cfg)
	if err != nil {
		return err
	}
	fmt.Println(successStyle.Render("Created PR: " + url))
	fmt.Println("pr-claim-change.yaml checks it; merging it applies the new size.")
	return nil
}

// resolveChange fills kind/name/source-path and checks the inputs claims can
// check without the rules (those stay in hack/claim-change.py).
func resolveChange(cfg *ChangeConfig) error {
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	if cfg.Size == "" {
		return errors.New("--size is required")
	}
	if cfg.SourcePath == "" || cfg.Kind == "" || cfg.Name == "" {
		root, err := findRepoRoot(".")
		if err != nil {
			if cfg.SourcePath == "" {
				return errors.New("--source-path, --kind and --name are needed outside a checkout of the fleet repository")
			}
			return errors.New("--kind and --name are needed outside a checkout of the fleet repository")
		}
		x := &DeleteXRConfig{Kind: cfg.Kind, Name: cfg.Name, Namespace: cfg.Namespace, SourcePath: cfg.SourcePath}
		if err := resolveXR(root, x); err != nil {
			return err
		}
		cfg.Kind, cfg.Name, cfg.Namespace, cfg.SourcePath = x.Kind, x.Name, x.Namespace, x.SourcePath
	}
	if err := xrdelete.CheckPath(cfg.SourcePath); err != nil {
		return err
	}
	if !changeVMKinds[cfg.Kind] {
		return fmt.Errorf("%s cannot be changed this way: only NativeVsphereVM and NativeProxmoxVM are resized", cfg.Kind)
	}
	switch cfg.Reboot {
	case "", "evening":
		cfg.Reboot = "evening"
	case "now", "none":
	case "at":
		if cfg.RebootAt == "" {
			return errors.New("--reboot at needs a time: pass it as --reboot <ISO-8601 time>")
		}
	default:
		if _, err := parseRebootTime(cfg.Reboot); err != nil {
			return fmt.Errorf("--reboot %q: want now, evening, none or an ISO-8601 time (%v)", cfg.Reboot, err)
		}
		cfg.RebootAt, cfg.Reboot = cfg.Reboot, "at"
	}
	return nil
}

func parseRebootTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("not an ISO-8601 time")
}

// changeInputs are the workflow_dispatch inputs, in the workflow's order.
func changeInputs(cfg *ChangeConfig) [][2]string {
	in := [][2]string{
		{"kind", cfg.Kind},
		{"name", cfg.Name},
		{"namespace", cfg.Namespace},
		{"source_path", cfg.SourcePath},
		{"size", cfg.Size},
		{"reboot", cfg.Reboot},
	}
	if cfg.RebootAt != "" {
		in = append(in, [2]string{"reboot_at", cfg.RebootAt})
	}
	if cfg.RequestedBy != "" {
		in = append(in, [2]string{"requested_by", cfg.RequestedBy})
	}
	if cfg.Reason != "" {
		in = append(in, [2]string{"reason", cfg.Reason})
	}
	return in
}

func repoArgs(cfg *ChangeConfig) []string {
	if cfg.Repo == "" {
		return nil
	}
	return []string{"-R", cfg.Repo}
}

func printChange(cfg *ChangeConfig, inputs [][2]string) {
	repo := cfg.Repo
	if repo == "" {
		repo = "(current checkout)"
	}
	fmt.Printf("Workflow: %s on %s, repository %s\n", changeWorkflow, cfg.Ref, repo)
	for _, kv := range inputs {
		fmt.Printf("  %-13s %s\n", kv[0]+":", kv[1])
	}
	fmt.Printf("PR:       change-claim/%s\n", cfg.Name)
}

type ghRun struct {
	ID         int64     `json:"databaseId"`
	CreatedAt  time.Time `json:"createdAt"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
}

// findDispatchedRun returns the first workflow_dispatch run of the workflow
// created after `after` (gh workflow run does not print the run).
func findDispatchedRun(cfg *ChangeConfig, after time.Time) (int64, error) {
	for i := 0; i < 30; i++ {
		out, err := gh(append([]string{"run", "list", "--workflow", changeWorkflow, "--event", "workflow_dispatch",
			"--limit", "10", "--json", "databaseId,createdAt,status,conclusion"}, repoArgs(cfg)...)...)
		if err != nil {
			return 0, err
		}
		var runs []ghRun
		if err := json.Unmarshal(out, &runs); err != nil {
			return 0, fmt.Errorf("parsing gh run list: %w", err)
		}
		var best *ghRun
		for i := range runs {
			r := &runs[i]
			if r.CreatedAt.After(after) && (best == nil || r.CreatedAt.Before(best.CreatedAt)) {
				best = r
			}
		}
		if best != nil {
			return best.ID, nil
		}
		time.Sleep(changePoll)
	}
	return 0, fmt.Errorf("the %s run did not show up; look at the repository's Actions tab", changeWorkflow)
}

func waitRun(cfg *ChangeConfig, id int64) (string, error) {
	for i := 0; i < 180; i++ {
		out, err := gh(append([]string{"run", "view", fmt.Sprint(id), "--json", "status,conclusion"}, repoArgs(cfg)...)...)
		if err != nil {
			return "", err
		}
		var r ghRun
		if err := json.Unmarshal(out, &r); err != nil {
			return "", fmt.Errorf("parsing gh run view: %w", err)
		}
		if r.Status == "completed" {
			return r.Conclusion, nil
		}
		time.Sleep(changePoll)
	}
	return "", fmt.Errorf("run %d did not finish in time", id)
}

func changePR(cfg *ChangeConfig) (string, error) {
	for i := 0; i < 6; i++ {
		out, err := gh(append([]string{"pr", "list", "--head", "change-claim/" + cfg.Name, "--state", "open",
			"--json", "url"}, repoArgs(cfg)...)...)
		if err != nil {
			return "", err
		}
		var prs []struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(out, &prs); err != nil {
			return "", fmt.Errorf("parsing gh pr list: %w", err)
		}
		if len(prs) > 0 {
			return prs[0].URL, nil
		}
		time.Sleep(changePoll)
	}
	return "", fmt.Errorf("the run succeeded but no open PR change-claim/%s was found", cfg.Name)
}

// refusalLines picks claim-change.py's ::error:: lines out of a failed log.
func refusalLines(log string) string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		if i := strings.Index(l, "::error::"); i >= 0 {
			out = append(out, "  "+strings.TrimSpace(l[i+len("::error::"):]))
		}
	}
	if len(out) == 0 {
		return "  (see the run's log)"
	}
	return strings.Join(out, "\n")
}

// pickChangeInteractive asks for the VM, the size and the reboot window.
func pickChangeInteractive(cfg *ChangeConfig) (bool, error) {
	if cfg.Name == "" && cfg.SourcePath == "" {
		root, err := findRepoRoot(".")
		if err != nil {
			return false, errors.New("run claims change interactively in a checkout of the fleet repository")
		}
		all, err := xrdelete.Scan(root)
		if err != nil {
			return false, err
		}
		var options []huh.Option[string]
		for _, c := range all {
			if changeVMKinds[c.Kind] {
				options = append(options, huh.NewOption(fmt.Sprintf("%-16s %s/%s  (%s)", c.Kind, c.Namespace, c.Name, c.File), c.File))
			}
		}
		if len(options) == 0 {
			fmt.Println("No VM claims in the *-xrs directories.")
			return false, nil
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Select the VM to resize").Options(options...).Height(15).Value(&cfg.SourcePath),
		)).Run(); err != nil {
			return false, err
		}
	}
	if cfg.Size == "" {
		cfg.Size = "large"
	}
	var confirm bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("New size").Options(
			huh.NewOption("small", "small"), huh.NewOption("medium", "medium"),
			huh.NewOption("large", "large"), huh.NewOption("xlarge", "xlarge"),
		).Value(&cfg.Size),
		huh.NewSelect[string]().Title("Apply in the guest").Options(
			huh.NewOption("evening (19-22 Europe/Berlin)", "evening"),
			huh.NewOption("now, right after the merge", "now"),
			huh.NewOption("none (reboot and grow by hand)", "none"),
		).Value(&cfg.Reboot),
		huh.NewInput().Title("Reason (optional)").Value(&cfg.Reason),
		huh.NewConfirm().Title("Open the change-claim PR?").Affirmative("Yes").Negative("Cancel").Value(&confirm),
	)).Run(); err != nil {
		return false, err
	}
	if !confirm {
		fmt.Println("Cancelled.")
	}
	return confirm, nil
}
