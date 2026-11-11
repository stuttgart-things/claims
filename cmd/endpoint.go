package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/endpoint"
)

// claims endpoint (claims#116): named claim-machinery-api endpoints in
// ~/.config/claims/config.yaml, like kube contexts. The API URL a command
// talks to is, in this order: -a/--api-url, $CLAIM_API_URL, --endpoint /
// $CLAIM_ENDPOINT, the current endpoint. There is no implicit localhost.

// endpointFlag is --endpoint of every command that talks to the API.
var endpointFlag string

// addEndpointFlag registers --endpoint on c.
func addEndpointFlag(c *cobra.Command) {
	c.Flags().StringVar(&endpointFlag, "endpoint", "", "Named API endpoint from `claims endpoint list` (or $"+endpoint.EnvEndpoint+")")
}

// apiTarget is the resolved API: its URL and, when it is a configured
// endpoint, its name and settings.
type apiTarget struct {
	URL      string // for render: may list several, colon-separated ($CLAIM_API_URL)
	Name     string // endpoint name; "" when the URL matches none
	Source   string // where it came from, for messages
	Endpoint endpoint.Endpoint
}

// String is "name (url)" or the URL.
func (t apiTarget) String() string {
	if t.Name != "" {
		return fmt.Sprintf("%s (%s)", t.Name, t.URL)
	}
	return t.URL
}

// loginArgs is how to name the target to claims login.
func (t apiTarget) loginArgs() string {
	if t.Name != "" {
		return "--endpoint " + t.Name
	}
	return "-a " + t.URL
}

// fixError is an error that knows what fixes it (the preflight prints both).
type fixError struct {
	msg, fix string
}

func (e *fixError) Error() string { return e.msg + "\n" + e.fix }

// errNoEndpoint replaces the silent fallback to http://localhost:8080
// (claims#114: a forgotten export hit a local server and returned 404).
var errNoEndpoint = &fixError{
	msg: "no claim-machinery-api endpoint chosen (there is no localhost default)",
	fix: "pass -a <url> or --endpoint <name>, set CLAIM_API_URL or CLAIM_ENDPOINT, or configure one: " +
		"claims endpoint add <name> <url> --use",
}

// endpointConfig loads the endpoint config file.
func endpointConfig() (*endpoint.Config, string, error) {
	path, err := endpoint.DefaultPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := endpoint.Load(path)
	return cfg, path, err
}

// pickEndpoint asks for the endpoint when nothing names one (interactive
// only); a variable for tests.
var pickEndpoint = func(cfg *endpoint.Config) (string, error) {
	if len(cfg.Endpoints) == 0 {
		var url string
		err := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("claim-machinery-api URL").
				Description("No endpoint configured (claims endpoint add <name> <url> keeps one)").
				Placeholder("https://claim-machinery-api.example.com").
				Value(&url).
				Validate(func(s string) error {
					if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
						return errors.New("an http:// or https:// URL")
					}
					return nil
				}),
		)).Run()
		return url, err
	}
	var name string
	opts := make([]huh.Option[string], 0, len(cfg.Endpoints))
	for _, n := range cfg.Names() {
		opts = append(opts, huh.NewOption(fmt.Sprintf("%s  %s", n, cfg.Endpoints[n].URL), n))
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Select API endpoint").
			Description("No endpoint chosen: pass --endpoint, or `claims endpoint use <name>`").
			Options(opts...).
			Value(&name),
	)).Run()
	return name, err
}

// resolveAPITarget picks the API: flagURL (-a), $CLAIM_API_URL, --endpoint /
// $CLAIM_ENDPOINT, the current endpoint; else the picker when interactive,
// errNoEndpoint otherwise.
func resolveAPITarget(flagURL string, interactive bool) (apiTarget, error) {
	cfg, path, err := endpointConfig()
	if err != nil {
		return apiTarget{}, err
	}
	named, namedFrom := endpointFlag, "--endpoint"
	if named == "" {
		named, namedFrom = os.Getenv(endpoint.EnvEndpoint), "$"+endpoint.EnvEndpoint
	}

	byURL := func(url, source string) apiTarget {
		t := apiTarget{URL: url, Source: source}
		if n, e, ok := cfg.ByURL(url); ok {
			t.Name, t.Endpoint = n, e
		}
		return t
	}
	byName := func(name, source string) (apiTarget, error) {
		e, err := cfg.Get(name)
		if err != nil {
			return apiTarget{}, fmt.Errorf("%s: %w", source, err)
		}
		return apiTarget{URL: e.URL, Name: name, Source: source, Endpoint: e}, nil
	}

	switch {
	case flagURL != "":
		return byURL(flagURL, "--api-url"), nil
	case os.Getenv("CLAIM_API_URL") != "":
		if named != "" {
			warnf(fmt.Sprintf("%s %s is ignored: $CLAIM_API_URL is set and wins (unset it to use the endpoint)", namedFrom, named))
		}
		return byURL(os.Getenv("CLAIM_API_URL"), "$CLAIM_API_URL"), nil
	case named != "":
		return byName(named, namedFrom)
	case cfg.Current != "":
		return byName(cfg.Current, "current endpoint in "+path)
	case interactive:
		picked, err := pickEndpoint(cfg)
		if err != nil {
			return apiTarget{}, err
		}
		if _, ok := cfg.Endpoints[picked]; ok {
			return byName(picked, "picked")
		}
		return byURL(picked, "entered"), nil
	}
	return apiTarget{}, errNoEndpoint
}

// claims endpoint ...

var (
	endpointAddFlags endpoint.Endpoint
	endpointAddUse   bool
)

var endpointCmd = &cobra.Command{
	Use:     "endpoint",
	GroupID: groupAuth,
	Short:   "Manage named claim-machinery-api endpoints (list, use, add, remove)",
	Long: `Named claim-machinery-api endpoints, like kube contexts, kept in
~/.config/claims/config.yaml (or $` + endpoint.EnvConfigFile + `). Each has the API URL and,
for ` + "`claims login`" + `, the OIDC issuer, client ID and an optional CA file.

Which API a command talks to: -a/--api-url, $CLAIM_API_URL, --endpoint /
$` + endpoint.EnvEndpoint + `, then the current endpoint. With none of them, an
interactive command offers a picker and a non-interactive one fails; there is
no localhost default.`,
	Example: `  claims endpoint add labda https://claim-machinery-api.machinery.4sthings.tiab.ssc.sva.de \
    --issuer https://zitadel.sthings-infra.4sthings.tiab.ssc.sva.de --client-id <id>@claims --use
  claims endpoint list
  claims endpoint use labul
  claims login --endpoint labda
  claims render --endpoint labul`,
}

var endpointListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the endpoints (* = current)",
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runEndpointList(os.Stdout))
	},
}

var endpointUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Make an endpoint the current one",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(updateEndpoints(os.Stdout, func(c *endpoint.Config) (string, error) {
			return fmt.Sprintf("Current endpoint: %s (%s)", args[0], c.Endpoints[args[0]].URL), c.Use(args[0])
		}))
	},
}

var endpointAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Add (or replace) an endpoint",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runEndpointAdd(os.Stdout, args[0], args[1]))
	},
}

var endpointRemoveCmd = &cobra.Command{
	Use:     "remove <name>",
	Aliases: []string{"rm"},
	Short:   "Remove an endpoint",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(updateEndpoints(os.Stdout, func(c *endpoint.Config) (string, error) {
			return "Removed endpoint " + args[0], c.Remove(args[0])
		}))
	},
}

var endpointCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the endpoint commands use (and why)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		t, err := resolveAPITarget("", false)
		exitOn(err)
		fmt.Printf("%s  (from %s)\n", t, t.Source)
	},
}

func init() {
	f := endpointAddCmd.Flags()
	f.StringVar(&endpointAddFlags.Issuer, "issuer", "", "OIDC issuer for `claims login` (the lab's ZITADEL)")
	f.StringVar(&endpointAddFlags.ClientID, "client-id", "", "Client ID of the native ZITADEL app")
	f.StringVar(&endpointAddFlags.CAFile, "ca-file", "", "Extra PEM roots for the issuer")
	f.BoolVar(&endpointAddUse, "use", false, "Also make it the current endpoint")
	addEndpointFlag(endpointCurrentCmd)

	endpointCmd.AddCommand(endpointListCmd, endpointUseCmd, endpointAddCmd, endpointRemoveCmd, endpointCurrentCmd)
	rootCmd.AddCommand(endpointCmd)
}

// updateEndpoints loads the config, applies change and saves it.
func updateEndpoints(out io.Writer, change func(*endpoint.Config) (string, error)) error {
	cfg, path, err := endpointConfig()
	if err != nil {
		return err
	}
	msg, err := change(cfg)
	if err != nil {
		return err
	}
	if err := cfg.Save(path); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, msg)
	return nil
}

func runEndpointAdd(out io.Writer, name, url string) error {
	return updateEndpoints(out, func(c *endpoint.Config) (string, error) {
		e := endpointAddFlags
		e.URL = url
		if err := c.Add(name, e); err != nil {
			return "", err
		}
		msg := fmt.Sprintf("Added endpoint %s (%s)", name, c.Endpoints[name].URL)
		switch {
		case endpointAddUse:
			if err := c.Use(name); err != nil {
				return "", err
			}
			msg += "; it is the current endpoint"
		case c.Current == "":
			msg += fmt.Sprintf("\nMake it the default with: claims endpoint use %s", name)
		}
		if e.Issuer == "" {
			msg += fmt.Sprintf("\nFor `claims login --endpoint %s`, add --issuer and --client-id.", name)
		}
		return msg, nil
	})
}

func runEndpointList(out io.Writer) error {
	cfg, path, err := endpointConfig()
	if err != nil {
		return err
	}
	if len(cfg.Endpoints) == 0 {
		_, _ = fmt.Fprintf(out, "No endpoints in %s. Add one with: claims endpoint add <name> <url>\n", path)
		return nil
	}
	for _, n := range cfg.Names() {
		e := cfg.Endpoints[n]
		mark := " "
		if n == cfg.Current {
			mark = "*"
		}
		login := ""
		if e.Issuer != "" {
			login = "  login: " + e.Issuer
		}
		_, _ = fmt.Fprintf(out, "%s %-12s %s%s\n", mark, n, e.URL, login)
	}
	return nil
}
