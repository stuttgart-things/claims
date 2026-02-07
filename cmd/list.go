package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/stuttgart-things/claims/internal/registry"
)

var (
	listRegistryPath string
	listCategory     string
	listTemplate     string
	listOutput       string
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List claims from registry",
	Long:  `Lists all claims registered in claims/registry.yaml, with optional filtering by category or template.`,
	Run:   runList,
}

func init() {
	listCmd.Flags().StringVar(&listRegistryPath, "registry-path", "claims/registry.yaml", "Path to registry.yaml")
	listCmd.Flags().StringVar(&listCategory, "category", "", "Filter by category")
	listCmd.Flags().StringVar(&listTemplate, "template", "", "Filter by template")
	listCmd.Flags().StringVarP(&listOutput, "output", "o", "table", "Output format (table, json)")

	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) {
	// Resolve registry path
	registryPath := listRegistryPath

	// If not absolute, try relative to repo root
	if !filepath.IsAbs(registryPath) {
		cwd, err := os.Getwd()
		if err == nil {
			repoRoot, err := findRepoRoot(cwd)
			if err == nil {
				registryPath = filepath.Join(repoRoot, registryPath)
			}
		}
	}

	reg, err := registry.Load(registryPath)
	if err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Error loading registry: %v", err)))
		os.Exit(1)
	}

	entries := registry.FilterEntries(reg, listCategory, listTemplate)

	if len(entries) == 0 {
		fmt.Println("No claims found.")
		return
	}

	switch listOutput {
	case "json":
		printJSON(entries)
	default:
		printTable(entries)
	}
}

func printTable(entries []registry.ClaimEntry) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTEMPLATE\tCATEGORY\tNAMESPACE\tSTATUS\tCREATED BY\tSOURCE")
	fmt.Fprintln(w, "----\t--------\t--------\t---------\t------\t----------\t------")

	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Name, e.Template, e.Category, e.Namespace, e.Status, e.CreatedBy, e.Source)
	}

	w.Flush()
}

func printJSON(entries []registry.ClaimEntry) {
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Error marshalling JSON: %v", err)))
		os.Exit(1)
	}
	fmt.Println(string(data))
}
