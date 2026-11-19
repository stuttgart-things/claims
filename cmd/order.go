package cmd

// claims templates, claims clusters, claims order, claims orders: ordering
// through machinery-order-service without a git token
// (docs/adr/0002-order-through-the-order-service.md), each with an
// interactive and a programmatic face
// (docs/adr/0001-interactive-and-programmatic.md).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/orders"
	"github.com/stuttgart-things/claims/internal/params"
	"github.com/stuttgart-things/claims/internal/templates"
)

// EnvOrdersURL names the order service like --orders-url.
const EnvOrdersURL = "CLAIM_ORDERS_URL"

var (
	ordersURLFlag   string
	orderAPIURLFlag string
	outputFlag      string
	orderNonInter   bool

	orderCluster      string
	orderReason       string
	orderAutoMerge    bool
	orderParams       []string
	orderParamsFile   string
	orderWait         bool
	orderWaitTimeout  time.Duration
	ordersMine        bool
	ordersPhase       string
	ordersClusterFilt string
	watchUntil        string
	watchInterval     time.Duration
)

// interactiveMode is the ADR 0001 rule: interactive at a terminal unless
// --non-interactive or -o json says otherwise.
func interactiveMode() bool {
	return !orderNonInter && outputFlag != "json" && isTTY(os.Stdin)
}

func addOutputFlag(c *cobra.Command) {
	c.Flags().StringVarP(&outputFlag, "output", "o", "table", "Output format: table or json")
}

func checkOutput() error {
	if outputFlag != "table" && outputFlag != "json" {
		return usagef("--output must be table or json, not %q", outputFlag)
	}
	return nil
}

// addOrderServiceFlags registers how to reach and authenticate to the order
// service (and claim-machinery-api, for template definitions).
func addOrderServiceFlags(c *cobra.Command) {
	c.Flags().StringVar(&ordersURLFlag, "orders-url", "", "machinery-order-service URL (or $"+EnvOrdersURL+", or the endpoint's orders-url)")
	c.Flags().StringVarP(&orderAPIURLFlag, "api-url", "a", "", "claim-machinery-api URL, for the login and template definitions (default: $CLAIM_API_URL, --endpoint, the current endpoint)")
	addEndpointFlag(c)
	addAPIAuthFlags(c)
}

// orderService resolves the order service URL and its auth. The login is
// the one of the claim-machinery-api endpoint (same ZITADEL project), unless
// --api-token / client credentials are given.
type orderService struct {
	client *orders.Client
	api    apiTarget // zero when no API endpoint is known
	apiErr error
}

func resolveOrderService() (*orderService, error) {
	target, targetErr := resolveAPITarget(orderAPIURLFlag, false)
	url := ordersURLFlag
	if url == "" {
		url = os.Getenv(EnvOrdersURL)
	}
	if url == "" && targetErr == nil {
		url = target.Endpoint.OrdersURL
	}
	if url == "" {
		fix := "pass --orders-url <url>, set " + EnvOrdersURL + ", or add it to the endpoint: claims endpoint add <name> <api-url> --orders-url <url>"
		return nil, usagef("no machinery-order-service URL\n%s", fix)
	}
	explicit, err := resolveAPIAuth()
	if err != nil {
		return nil, err
	}
	auth := explicit
	if auth == nil && targetErr == nil {
		auth = loginAuthFor(splitAPIURLs(target.URL)[0])
	}
	if auth == nil {
		warnf("no login for the order service: run `claims login`, or pass --api-token / --api-oidc-*")
	}
	svc := &orderService{client: orders.New(url, auth.HTTPClient(30*time.Second)), api: target, apiErr: targetErr}
	return svc, nil
}

// templatesClient is the claim-machinery-api client for template
// definitions, or an error naming what is missing.
func (s *orderService) templatesClient() (*templates.Client, error) {
	if s.apiErr != nil {
		return nil, s.apiErr
	}
	auth, err := resolveAPIAuth()
	if err != nil {
		return nil, err
	}
	return newAPIClient(splitAPIURLs(s.api.URL)[0], auth), nil
}

// claims templates

var templatesCmd = &cobra.Command{
	Use:     "templates [name]",
	GroupID: groupClaims,
	Short:   "List the claim templates, or show one template's parameters",
	Long: `Lists the claim templates of claim-machinery-api with title and
description, or with a name the template's parameters (required, default,
allowed values, when they apply). Needs only the API login, no repository.`,
	Example: `  claims templates
  claims templates native-vm-labul-proxmox
  claims templates -o json`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runTemplates(os.Stdout, args))
	},
}

func runTemplates(out io.Writer, args []string) error {
	if err := checkOutput(); err != nil {
		return err
	}
	target, err := resolveAPITarget(orderAPIURLFlag, false)
	if err != nil {
		return err
	}
	auth, err := resolveAPIAuth()
	if err != nil {
		return err
	}
	list, err := newAPIClient(splitAPIURLs(target.URL)[0], auth).FetchTemplates()
	if err != nil {
		return err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Metadata.Name < list[j].Metadata.Name })
	if len(args) == 1 {
		for i := range list {
			if list[i].Metadata.Name == args[0] {
				return printTemplate(out, &list[i])
			}
		}
		return &templates.APIError{Status: 404, Message: fmt.Sprintf("no template %q (list them: claims templates)", args[0])}
	}
	if outputFlag == "json" {
		return writeJSON(out, list)
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tTYPE\tTITLE")
	for _, t := range list {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Metadata.Name, t.Spec.Type, firstNonEmpty(t.Metadata.Title, t.Metadata.Description))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(out, "\nParameters of one: claims templates <name>; order: claims order <name>")
	return nil
}

func printTemplate(out io.Writer, t *templates.ClaimTemplate) error {
	if outputFlag == "json" {
		return writeJSON(out, t)
	}
	_, _ = fmt.Fprintf(out, "%s  (%s)\n", t.Metadata.Name, t.Spec.Type)
	if d := firstNonEmpty(t.Metadata.Description, t.Metadata.Title); d != "" {
		_, _ = fmt.Fprintf(out, "%s\n", d)
	}
	_, _ = fmt.Fprintln(out)
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "PARAMETER\tREQUIRED\tDEFAULT\tALLOWED\tWHEN")
	for _, p := range t.Spec.Parameters {
		if p.Hidden {
			continue
		}
		req := ""
		if p.Required {
			req = "yes"
		}
		def := ""
		if p.Default != nil {
			def = fmt.Sprint(p.Default)
		}
		allowed := strings.Join(p.Enum, ", ")
		if allowed == "" && p.Pattern != "" {
			allowed = "pattern " + p.Pattern
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, req, def, allowed, showWhenText(p.ShowWhen))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(out, "\nOrder it: claims order %s --cluster <name> -p key=value ... (clusters: claims clusters)\n", t.Metadata.Name)
	return nil
}

func showWhenText(w map[string][]string) string {
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strings.Join(w[k], "|"))
	}
	return strings.Join(parts, " ")
}

// claims clusters

var clustersCmd = &cobra.Command{
	Use:     "clusters",
	GroupID: groupClaims,
	Short:   "List the management clusters orders can go to",
	Example: `  claims clusters
  claims clusters -o json`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runClusters(os.Stdout))
	},
}

func runClusters(out io.Writer) error {
	if err := checkOutput(); err != nil {
		return err
	}
	svc, err := resolveOrderService()
	if err != nil {
		return err
	}
	cs, err := svc.client.Clusters()
	if err != nil {
		return err
	}
	if outputFlag == "json" {
		return writeJSON(out, cs)
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tLAB\tTAKES ORDERS\tAUTO-MERGE\tORDERS PATH")
	var rules []string
	for _, c := range cs {
		takes := "yes"
		if c.Cordoned {
			takes = "no (cordoned)"
		}
		am := "-"
		if c.AutoMerge != nil && len(c.AutoMerge.Rules) > 0 {
			am = fmt.Sprintf("%d rule(s)", len(c.AutoMerge.Rules))
			for _, r := range c.AutoMerge.Rules {
				rules = append(rules, "  "+c.Name+"  "+r.String())
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Name, c.Lab, takes, am, c.OrdersPath)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(rules) > 0 {
		_, _ = fmt.Fprintln(out, "\nAuto-merge rules (claims order --auto-merge):")
		for _, r := range rules {
			_, _ = fmt.Fprintln(out, r)
		}
	}
	return nil
}

// claims order

var orderCmd = &cobra.Command{
	Use:     "order [template]",
	GroupID: groupClaims,
	Short:   "Order a claim through machinery-order-service (no git token needed)",
	Long: `Orders a claim through machinery-order-service. The service renders it
with your login, opens the pull request with its own GitHub identity and
follows the order through merge and build; claims never touches git here.

At a terminal, claims asks for whatever is missing (template, cluster,
parameters) and confirms before ordering. Without a terminal, or with
--non-interactive / -o json, it never asks: template, --cluster and the
template's required parameters (-p / -f) must be given, and a missing one is
an error naming the flag.

The pull request waits for a human to merge it. --auto-merge asks the
service to merge it itself once its checks pass; the service allows that
only when a rule of the cluster covers you, the template and the parameters
(claims clusters shows the rules), and refuses the order otherwise.

The result is the order ID and its phase. Follow it with
'claims orders watch <id>', or pass --wait.`,
	Example: `  claims order                                   # interactive
  claims order native-vm-labul-proxmox --cluster machinery-labul \
    -p name=demo -p size=small --reason "test VM"
  claims order native-vm-labul-proxmox --cluster machinery-labul -f params.yaml --wait -o json
  claims order native-vm-labul-proxmox --cluster machinery-labul \
    -p name=demo -p size=small --auto-merge --wait   # if a rule allows it`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runOrder(os.Stdout, args))
	},
}

func runOrder(out io.Writer, args []string) error {
	if err := checkOutput(); err != nil {
		return err
	}
	interactive := interactiveMode()
	svc, err := resolveOrderService()
	if err != nil {
		return err
	}
	req := orders.CreateRequest{ManagementCluster: orderCluster, Reason: orderReason, AutoMerge: orderAutoMerge}
	if len(args) == 1 {
		req.Template = args[0]
	}

	p := map[string]any{}
	if orderParamsFile != "" {
		pf, err := params.ParseFile(orderParamsFile)
		if err != nil {
			return usagef("--params-file: %v", err)
		}
		if len(pf.Templates) > 1 {
			return usagef("--params-file: claims order takes one template, the file has %d", len(pf.Templates))
		}
		if len(pf.Templates) == 1 {
			if req.Template == "" {
				req.Template = pf.Templates[0].Name
			}
			p = params.MergeParams(p, pf.Templates[0].Parameters)
		}
	}
	inline, err := params.ParseInlineParams(orderParams)
	if err != nil {
		return usagef("-p: %v", err)
	}
	p = params.MergeParams(p, inline)

	// Template definitions: for the picker and the form (interactive), and to
	// drop parameters that do not apply. Without claim-machinery-api the
	// service still validates.
	var tmpl *templates.ClaimTemplate
	if tc, terr := svc.templatesClient(); terr == nil {
		if list, lerr := tc.FetchTemplates(); lerr == nil {
			if req.Template == "" && interactive {
				name, perr := pickTemplate(list)
				if perr != nil {
					return perr
				}
				req.Template = name
			}
			for i := range list {
				if list[i].Metadata.Name == req.Template {
					tmpl = &list[i]
				}
			}
			if req.Template != "" && tmpl == nil {
				return &templates.APIError{Status: 404, Message: fmt.Sprintf("no template %q (list them: claims templates)", req.Template)}
			}
		} else if interactive {
			return lerr
		}
	} else if interactive {
		return terr
	}
	if req.Template == "" {
		return usagef("a template is required: claims order <template> (list them: claims templates)")
	}
	if tmpl != nil && len(tmpl.Spec.Secrets) > 0 {
		return fmt.Errorf("template %s has secrets, which are encrypted on your machine: order it with claims render --create-pr", tmpl.Metadata.Name)
	}

	if req.ManagementCluster == "" {
		if !interactive {
			return usagef("--cluster is required (list them: claims clusters)")
		}
		cs, err := svc.client.Clusters()
		if err != nil {
			return err
		}
		name, err := pickCluster(cs)
		if err != nil {
			return err
		}
		req.ManagementCluster = name
	}

	if tmpl != nil {
		if interactive {
			form := newParamForm(tmpl, p)
			p, err = form.run(nil)
			if err != nil {
				return err
			}
			if req.Reason == "" {
				if err := huh.NewInput().Title("Reason (optional)").Description("Shown in the pull request").Value(&req.Reason).Run(); err != nil {
					return err
				}
			}
		} else {
			if missing := missingRequired(tmpl, p); len(missing) > 0 {
				return usagef("missing required parameters: %s (pass -p name=value; see claims templates %s)", strings.Join(missing, ", "), tmpl.Metadata.Name)
			}
		}
		p = dropInactive(io.Discard, tmpl, p)
	}
	req.Parameters = p

	if interactive {
		ok := true
		summary := fmt.Sprintf("%s on %s with %d parameter(s)", req.Template, req.ManagementCluster, len(p))
		if req.AutoMerge {
			summary += ", auto-merge"
		}
		if err := huh.NewConfirm().Title("Order " + summary + "?").Affirmative("Order").Negative("Cancel").Value(&ok).Run(); err != nil {
			return err
		}
		if !ok {
			return errAborted
		}
	}

	o, err := svc.client.Create(req)
	if err != nil {
		return err
	}
	if orderWait {
		if outputFlag != "json" {
			printOrderLine(out, o)
		}
		o, err = watchOrder(out, svc.client, o.ID(), "", orderWaitTimeout, watchInterval, outputFlag != "json")
		if err != nil {
			return err
		}
	}
	if outputFlag == "json" {
		return writeRaw(out, o.Raw)
	}
	if !orderWait {
		printOrderLine(out, o)
		_, _ = fmt.Fprintf(out, "Follow it: claims orders watch %s\n", o.ID())
	}
	return nil
}

// missingRequired lists required, active parameters without a value or a
// default, in template order.
func missingRequired(t *templates.ClaimTemplate, p map[string]any) []string {
	active := dropInactive(io.Discard, t, withDefaults(t, p))
	var out []string
	for _, prm := range t.Spec.Parameters {
		if !prm.Required || prm.Hidden || prm.ValueFrom != nil {
			continue
		}
		if len(prm.ShowWhen) > 0 && !conditionsMet(prm.ShowWhen, active) {
			continue
		}
		if v, ok := p[prm.Name]; ok && fmt.Sprint(v) != "" {
			continue
		}
		if prm.Default != nil {
			continue
		}
		out = append(out, prm.Name)
	}
	return out
}

func withDefaults(t *templates.ClaimTemplate, p map[string]any) map[string]any {
	out := map[string]any{}
	for _, prm := range t.Spec.Parameters {
		if prm.Default != nil {
			out[prm.Name] = prm.Default
		}
	}
	for k, v := range p {
		out[k] = v
	}
	return out
}

func conditionsMet(when map[string][]string, values map[string]any) bool {
	for k, allowed := range when {
		v := fmt.Sprint(values[k])
		ok := false
		for _, a := range allowed {
			if a == v {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func pickTemplate(list []templates.ClaimTemplate) (string, error) {
	opts := make([]huh.Option[string], 0, len(list))
	for _, t := range list {
		label := t.Metadata.Name
		if d := firstNonEmpty(t.Metadata.Title, t.Metadata.Description); d != "" {
			label += "  " + d
		}
		opts = append(opts, huh.NewOption(label, t.Metadata.Name))
	}
	var name string
	err := huh.NewSelect[string]().Title("Template").Options(opts...).Value(&name).Run()
	return name, err
}

func pickCluster(cs []orders.Cluster) (string, error) {
	opts := []huh.Option[string]{}
	for _, c := range cs {
		if c.Cordoned {
			continue
		}
		label := c.Name
		if c.Lab != "" {
			label += "  (" + c.Lab + ")"
		}
		opts = append(opts, huh.NewOption(label, c.Name))
	}
	if len(opts) == 0 {
		return "", errors.New("no management cluster takes orders")
	}
	var name string
	err := huh.NewSelect[string]().Title("Management cluster").Options(opts...).Value(&name).Run()
	return name, err
}

// claims orders ...

var ordersCmd = &cobra.Command{
	Use:     "orders",
	GroupID: groupClaims,
	Short:   "Follow orders placed through machinery-order-service (list, get, watch, cancel)",
	Long: `Follow orders: list the ones you may see (yours and your teams'),
show one, watch it until it is Ready (or fails), cancel it before the merge.

Phases: Ordered, PROpen (pull request open, waits for a merge), Merged,
Applied, Building, Ready; or Cancelled, Failed, Deleted.`,
	Example: `  claims orders list
  claims orders get ord-0123456789ab
  claims orders watch ord-0123456789ab
  claims orders cancel ord-0123456789ab`,
}

var ordersListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List the orders you may see, newest first",
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runOrdersList(os.Stdout))
	},
}

func runOrdersList(out io.Writer) error {
	if err := checkOutput(); err != nil {
		return err
	}
	svc, err := resolveOrderService()
	if err != nil {
		return err
	}
	list, err := svc.client.List(orders.ListOptions{Mine: ordersMine, Phase: ordersPhase, Cluster: ordersClusterFilt})
	if err != nil {
		return err
	}
	if outputFlag == "json" {
		raws := make([]json.RawMessage, 0, len(list))
		for _, o := range list {
			raws = append(raws, o.Raw)
		}
		return writeJSON(out, raws)
	}
	if len(list) == 0 {
		_, _ = fmt.Fprintln(out, "No orders.")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tTEMPLATE\tCLUSTER\tPHASE\tAGE\tREQUESTED BY\tPR")
	for _, o := range list {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", o.ID(), o.Spec.Template, o.Spec.Placement.ManagementCluster,
			o.Phase(), age(o.Metadata.CreationTimestamp), o.Spec.RequestedBy, o.PRURL())
	}
	return tw.Flush()
}

var ordersGetCmd = &cobra.Command{
	Use:   "get <id>",
	Short: "Show one order: phase, pull request, address, timeline",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runOrdersGet(os.Stdout, args[0]))
	},
}

func runOrdersGet(out io.Writer, id string) error {
	if err := checkOutput(); err != nil {
		return err
	}
	svc, err := resolveOrderService()
	if err != nil {
		return err
	}
	o, err := svc.client.Get(id)
	if err != nil {
		return err
	}
	if outputFlag == "json" {
		return writeRaw(out, o.Raw)
	}
	printOrder(out, o)
	return nil
}

var ordersWatchCmd = &cobra.Command{
	Use:   "watch <id>",
	Short: "Print the order's phase changes until it is Ready, Failed, Cancelled or Deleted",
	Long: `Polls the order and prints each phase change. Ends at a final phase
(Ready, Failed, Cancelled, Deleted), at --until, or after --timeout. Exit
code 1 when it ends Failed or Cancelled, or times out.

PROpen waits for someone to merge the pull request.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runOrdersWatch(os.Stdout, args[0]))
	},
}

func runOrdersWatch(out io.Writer, id string) error {
	if err := checkOutput(); err != nil {
		return err
	}
	svc, err := resolveOrderService()
	if err != nil {
		return err
	}
	o, err := watchOrder(out, svc.client, id, watchUntil, orderWaitTimeout, watchInterval, outputFlag != "json")
	if err != nil {
		return err
	}
	if outputFlag == "json" {
		return writeRaw(out, o.Raw)
	}
	return nil
}

// watchOrder polls id until a final phase (or until), printing each change
// when verbose.
func watchOrder(out io.Writer, c *orders.Client, id, until string, timeout, interval time.Duration, verbose bool) (*orders.Order, error) {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		o, err := c.Get(id)
		if err != nil {
			return nil, err
		}
		if ph := o.Phase(); ph != last {
			if verbose {
				line := fmt.Sprintf("%s  %-9s %s", time.Now().UTC().Format("15:04:05"), ph, o.Status.Message)
				if ph == "PROpen" && o.PRURL() != "" {
					if o.Spec.AutoMerge != nil {
						line += "  " + o.PRURL() + " (auto-merge once its checks pass)"
					} else {
						line += "  " + o.PRURL() + " (waits for a merge)"
					}
				}
				if ph == "Ready" && o.Address() != "" {
					line += "  address " + o.Address()
				}
				_, _ = fmt.Fprintln(out, strings.TrimRight(line, " "))
			}
			last = ph
		}
		switch {
		case until != "" && o.Phase() == until:
			return o, nil
		case o.Phase() == "Failed" || o.Phase() == "Cancelled":
			return o, fmt.Errorf("order %s ended %s: %s", id, o.Phase(), o.Status.Message)
		case orders.IsFinal(o.Phase()):
			return o, nil
		case time.Now().After(deadline):
			return o, fmt.Errorf("order %s is still %s after %s (claims orders watch %s goes on)", id, o.Phase(), timeout, id)
		}
		time.Sleep(interval)
	}
}

var ordersCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Withdraw an order before its pull request is merged",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runOrdersCancel(os.Stdout, args[0]))
	},
}

func runOrdersCancel(out io.Writer, id string) error {
	if err := checkOutput(); err != nil {
		return err
	}
	svc, err := resolveOrderService()
	if err != nil {
		return err
	}
	if interactiveMode() {
		ok := false
		if err := huh.NewConfirm().Title("Cancel order " + id + "?").Affirmative("Cancel it").Negative("Keep it").Value(&ok).Run(); err != nil {
			return err
		}
		if !ok {
			return errAborted
		}
	}
	o, err := svc.client.Cancel(id)
	if err != nil {
		return err
	}
	if outputFlag == "json" {
		return writeRaw(out, o.Raw)
	}
	_, _ = fmt.Fprintf(out, "Cancel requested for %s; the pull request is closed and the order ends Cancelled.\n", id)
	return nil
}

// output helpers

func printOrderLine(out io.Writer, o *orders.Order) {
	_, _ = fmt.Fprintf(out, "Order %s: %s on %s, phase %s", o.ID(), o.Spec.Template, o.Spec.Placement.ManagementCluster, o.Phase())
	if o.Status.Message != "" {
		_, _ = fmt.Fprintf(out, " (%s)", o.Status.Message)
	}
	_, _ = fmt.Fprintln(out)
	if am := o.Spec.AutoMerge; am != nil {
		_, _ = fmt.Fprintf(out, "Auto-merge: the service merges the pull request once its checks pass (rule %s)\n", am.Rule)
	}
}

func printOrder(out io.Writer, o *orders.Order) {
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	row := func(k, v string) {
		if v != "" {
			_, _ = fmt.Fprintf(tw, "%s:\t%s\n", k, v)
		}
	}
	row("Order", o.ID())
	row("Template", o.Spec.Template)
	row("Cluster", o.Spec.Placement.ManagementCluster)
	row("Phase", o.Phase())
	row("Message", o.Status.Message)
	row("Requested by", o.Spec.RequestedBy)
	row("Reason", o.Spec.Reason)
	if am := o.Spec.AutoMerge; am != nil {
		row("Auto-merge", "yes, rule "+am.Rule)
	}
	row("Pull request", o.PRURL())
	row("Address", o.Address())
	row("Created", o.Metadata.CreationTimestamp.UTC().Format(time.RFC3339))
	_ = tw.Flush()
	if len(o.Status.History) > 0 {
		_, _ = fmt.Fprintln(out, "Timeline:")
		for _, h := range o.Status.History {
			_, _ = fmt.Fprintf(out, "  %s  %-9s %s\n", h.Time.UTC().Format("2006-01-02 15:04:05"), h.Phase, h.Message)
		}
	}
}

func age(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeRaw(out io.Writer, raw json.RawMessage) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	return writeJSON(out, v)
}

func init() {
	for _, c := range []*cobra.Command{templatesCmd} {
		c.Flags().StringVarP(&orderAPIURLFlag, "api-url", "a", "", "claim-machinery-api URL (default: $CLAIM_API_URL, --endpoint, the current endpoint)")
		addEndpointFlag(c)
		addAPIAuthFlags(c)
		addOutputFlag(c)
	}
	for _, c := range []*cobra.Command{clustersCmd, orderCmd, ordersListCmd, ordersGetCmd, ordersWatchCmd, ordersCancelCmd} {
		addOrderServiceFlags(c)
		addOutputFlag(c)
		c.Flags().BoolVar(&orderNonInter, "non-interactive", false, "Never ask (default without a terminal)")
	}
	f := orderCmd.Flags()
	f.StringVar(&orderCluster, "cluster", "", "Management cluster (list them: claims clusters)")
	f.StringVar(&orderReason, "reason", "", "Why, shown in the pull request")
	f.BoolVar(&orderAutoMerge, "auto-merge", false, "Let the service merge the pull request once its checks pass (only where a rule of the cluster allows it; see claims clusters)")
	f.StringSliceVarP(&orderParams, "param", "p", nil, "Parameter key=value (repeatable)")
	f.StringVarP(&orderParamsFile, "params-file", "f", "", "YAML/JSON file with template and parameters")
	f.BoolVar(&orderWait, "wait", false, "Wait until the order is Ready (or fails); PROpen waits for a merge")
	f.DurationVar(&orderWaitTimeout, "timeout", 60*time.Minute, "How long --wait waits")

	lf := ordersListCmd.Flags()
	lf.BoolVar(&ordersMine, "mine", false, "Only the orders you placed (not your teams')")
	lf.StringVar(&ordersPhase, "phase", "", "Only orders in this phase")
	lf.StringVar(&ordersClusterFilt, "cluster", "", "Only orders on this management cluster")

	wf := ordersWatchCmd.Flags()
	wf.StringVar(&watchUntil, "until", "", "Stop at this phase, e.g. PROpen")
	wf.DurationVar(&orderWaitTimeout, "timeout", 60*time.Minute, "Give up after this long")
	wf.DurationVar(&watchInterval, "interval", 10*time.Second, "Poll interval")

	ordersCmd.AddCommand(ordersListCmd, ordersGetCmd, ordersWatchCmd, ordersCancelCmd)
	rootCmd.AddCommand(templatesCmd, clustersCmd, orderCmd, ordersCmd)
}
