package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/stuttgart-things/claims/internal/secretpath"
)

var secretPathOutput string

var secretPathCmd = &cobra.Command{
	Use:   "path <vault-path|file>",
	Short: "Convert a Vault path to its SOPS file and back",
	Long: `Prints the counterpart of a Vault path or a SOPS file, 1:1 and without any
mapping configuration:

  secrets/<lab>/<mount>/<path>.enc.yaml   <=>   <mount>/<path> in the Vault of <lab>

A Vault path needs the lab (--lab, $CLAIMS_LAB or the current lab). A
directory or a prefix ending in '/' converts as a tree. -o yaml|json prints
every field, including the metadata.name of the Secret:

  metadata.name  every segment of <lab>/<mount>/<path> a DNS-1123 label without
                 "--": the segments joined by '.' (labul.kv.app.db); otherwise
                 a lowercased slug plus "--" and 10 hex digits of the sha256 of
                 the path (labul.kv.app-creds--1a2b3c4d5e).`,
	Example: `  claims secret path kv/machinery-dev/argocd-oidc --lab labul
  # secrets/labul/kv/machinery-dev/argocd-oidc.enc.yaml
  claims secret path secrets/labul/kv/machinery-dev/argocd-oidc.enc.yaml
  # kv/machinery-dev/argocd-oidc
  claims secret path kv/machinery-dev/ --lab labul -o yaml`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		exitOn(runSecretPath(args[0], secretPathOutput, os.Stdout))
	},
}

func init() {
	secretPathCmd.Flags().StringVarP(&secretPathOutput, "output", "o", "", "Output format: '' (the counterpart only), yaml or json")
	secretCmd.AddCommand(secretPathCmd)
}

type secretPathInfo struct {
	Lab        string `json:"lab" yaml:"lab"`
	VaultPath  string `json:"vaultPath" yaml:"vaultPath"`
	File       string `json:"file,omitempty" yaml:"file,omitempty"`
	Dir        string `json:"dir,omitempty" yaml:"dir,omitempty"`
	SecretName string `json:"secretName,omitempty" yaml:"secretName,omitempty"`
	Namespace  string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

func runSecretPath(arg, output string, out io.Writer) error {
	// No checkout needed: paths are relative to the repository root. A
	// directory argument only resolves against a checkout when there is one.
	root, _ := findRepoRoot(secretOpts.repo)
	var info secretPathInfo
	fromFile := isFileArg(arg)
	if isTreeArg(root, arg) {
		t, err := resolveSecretTree(rootOrDot(root), arg)
		if err != nil {
			return err
		}
		info = secretPathInfo{Lab: t.Lab, VaultPath: t.VaultPath(), Dir: t.Dir()}
		if output == "" {
			if fromFile {
				_, _ = fmt.Fprintln(out, t.VaultPath())
			} else {
				_, _ = fmt.Fprintln(out, t.Dir()+"/")
			}
			return nil
		}
	} else {
		t, err := resolveSecretTarget(rootOrDot(root), arg)
		if err != nil {
			return err
		}
		info = secretPathInfo{Lab: t.Ref.Lab, VaultPath: t.Ref.VaultPath(), File: t.Rel,
			SecretName: secretpath.SecretName(t.Ref), Namespace: secretpath.DefaultNamespace}
		if output == "" {
			if fromFile {
				_, _ = fmt.Fprintln(out, t.Ref.VaultPath())
			} else {
				_, _ = fmt.Fprintln(out, t.Rel)
			}
			return nil
		}
	}
	switch output {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(info)
	case "yaml":
		return yaml.NewEncoder(out).Encode(info)
	default:
		return usagef("--output %q: use yaml or json", output)
	}
}

// rootOrDot is root, or "." outside a checkout.
func rootOrDot(root string) string {
	if root == "" {
		abs, err := os.Getwd()
		if err != nil {
			return "."
		}
		return abs
	}
	return root
}
