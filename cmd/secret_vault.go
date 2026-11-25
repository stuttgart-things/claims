package cmd

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/stuttgart-things/claims/internal/endpoint"
)

// claims secret vault add|list|use|remove: the labs' Vaults in
// ~/.config/claims/config.yaml, next to the API endpoints (claims#116).

var secretVaultFlags struct {
	caFile       string
	auth         string
	appRoleMount string
}

var secretVaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Configure the Vault of each lab (add, list, use, remove)",
	Long: `The Vault of each lab lives in ~/.config/claims/config.yaml ($CLAIMS_CONFIG_FILE)
under vaults:, keyed by the lab of secrets/<lab>/...:

  current-vault: labul
  vaults:
    labul:
      addr: https://vault.example.com
      ca-file: /etc/ssl/lab-ca.pem
      auth: approle          # token (default) or approle
      approle-mount: approle

Auth: token from --vault-token, $VAULT_TOKEN or ~/.vault-token; AppRole from
$VAULT_ROLE_ID and $VAULT_SECRET_ID (CI). OIDC is planned.`,
}

var secretVaultAddCmd = &cobra.Command{
	Use:     "add <lab> <addr>",
	Short:   "Add or replace the Vault of a lab",
	Example: `  claims secret vault add labul https://vault.infra.example.com --ca-file ~/lab-ca.pem`,
	Args:    cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(editClaimsConfig(func(c *endpoint.Config) error {
			if err := c.AddVault(args[0], endpoint.Vault{
				Addr: args[1], CAFile: secretVaultFlags.caFile, Auth: secretVaultFlags.auth, AppRoleMount: secretVaultFlags.appRoleMount,
			}); err != nil {
				return err
			}
			if c.CurrentVault == "" {
				c.CurrentVault = args[0]
			}
			_, _ = fmt.Fprintf(os.Stdout, "Vault of lab %s: %s\n", args[0], c.Vaults[args[0]].Addr)
			return nil
		}))
	},
}

var secretVaultListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the configured Vaults",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSecretVaultList(os.Stdout))
	},
}

var secretVaultUseCmd = &cobra.Command{
	Use:   "use <lab>",
	Short: "Make a lab the default of claims secret",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(editClaimsConfig(func(c *endpoint.Config) error { return c.UseVault(args[0]) }))
	},
}

var secretVaultRemoveCmd = &cobra.Command{
	Use:   "remove <lab>",
	Short: "Remove the Vault of a lab",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(editClaimsConfig(func(c *endpoint.Config) error { return c.RemoveVault(args[0]) }))
	},
}

func init() {
	f := secretVaultAddCmd.Flags()
	f.StringVar(&secretVaultFlags.caFile, "ca-file", "", "PEM file with the CA of the Vault")
	f.StringVar(&secretVaultFlags.auth, "auth", "", "Auth method: token or approle (default: token, else approle if $VAULT_ROLE_ID is set)")
	f.StringVar(&secretVaultFlags.appRoleMount, "approle-mount", "", "Mount of the AppRole auth method (default approle)")
	secretVaultCmd.AddCommand(secretVaultAddCmd, secretVaultListCmd, secretVaultUseCmd, secretVaultRemoveCmd)
	secretCmd.AddCommand(secretVaultCmd)
}

func editClaimsConfig(edit func(*endpoint.Config) error) error {
	p, err := endpoint.DefaultPath()
	if err != nil {
		return err
	}
	c, err := endpoint.Load(p)
	if err != nil {
		return err
	}
	if err := edit(c); err != nil {
		return err
	}
	return c.Save(p)
}

func runSecretVaultList(out io.Writer) error {
	c, err := loadClaimsConfig()
	if err != nil {
		return err
	}
	if len(c.Vaults) == 0 {
		_, _ = fmt.Fprintln(out, "No Vaults configured: claims secret vault add <lab> <addr>")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "\tLAB\tADDR\tAUTH\tCA FILE")
	for _, n := range c.VaultNames() {
		v := c.Vaults[n]
		mark := ""
		if n == c.CurrentVault {
			mark = "*"
		}
		auth := v.Auth
		if auth == "" {
			auth = "token"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", mark, n, v.Addr, auth, v.CAFile)
	}
	return tw.Flush()
}
