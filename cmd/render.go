package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

const randomMarker = "🎲 Random"

// API data types
type ClaimTemplate struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   ClaimTemplateMetadata `json:"metadata"`
	Spec       ClaimTemplateSpec     `json:"spec"`
}

type ClaimTemplateMetadata struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type ClaimTemplateSpec struct {
	Type       string      `json:"type"`
	Source     string      `json:"source"`
	Tag        string      `json:"tag,omitempty"`
	Parameters []Parameter `json:"parameters"`
}

type Parameter struct {
	Name        string      `json:"name"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Type        string      `json:"type"`
	Default     interface{} `json:"default,omitempty"`
	Required    bool        `json:"required,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Pattern     string      `json:"pattern,omitempty"`
	Hidden      bool        `json:"hidden,omitempty"`
	AllowRandom bool        `json:"allowRandom,omitempty"`
}

type ClaimTemplateList struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Items      []ClaimTemplate `json:"items"`
}

type OrderRequest struct {
	Parameters map[string]interface{} `json:"parameters"`
}

type OrderResponse struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Metadata   map[string]interface{} `json:"metadata"`
	Rendered   string                 `json:"rendered"`
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			MarginBottom(1)

	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("42"))

	yamlStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(1)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))
)

var apiURL string

var renderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render a claim template interactively",
	Long:  `Connects to the claim-machinery API, fetches available templates, and provides an interactive form to render claims.`,
	Run:   runRender,
}

func init() {
	renderCmd.Flags().StringVarP(&apiURL, "api-url", "a", "", "API URL (default: $CLAIM_API_URL or http://localhost:8080)")
	rootCmd.AddCommand(renderCmd)
}

func runRender(cmd *cobra.Command, args []string) {
	// Get API URL from flag, environment, or default
	if apiURL == "" {
		apiURL = os.Getenv("CLAIM_API_URL")
	}
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}

	fmt.Printf("Connecting to API: %s\n\n", apiURL)

	// Create HTTP client
	client := &http.Client{Timeout: 30 * time.Second}

	// Fetch templates from API
	templates, err := fetchTemplates(client, apiURL)
	if err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Failed to fetch templates: %v", err)))
		os.Exit(1)
	}

	fmt.Printf("Loaded %d templates from API\n\n", len(templates))

	// Build template map and options for selection
	templateMap := make(map[string]*ClaimTemplate)
	var templateOptions []huh.Option[string]

	for i, t := range templates {
		templateMap[t.Metadata.Name] = &templates[i]
		label := fmt.Sprintf("%s - %s", t.Metadata.Name, t.Metadata.Title)
		templateOptions = append(templateOptions, huh.NewOption(label, t.Metadata.Name))
	}

	// Step 1: Select template
	var selectedTemplate string
	selectForm := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Select a template").
				Description("Choose which claim template to render").
				Options(templateOptions...).
				Value(&selectedTemplate),
		),
	)

	if err := selectForm.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	tmpl := templateMap[selectedTemplate]
	fmt.Printf("\n%s\n", titleStyle.Render(tmpl.Metadata.Title))
	fmt.Printf("%s\n\n", tmpl.Metadata.Description)

	// Step 2: Build dynamic form based on template parameters
	params := make(map[string]interface{})
	paramValues := make(map[string]*string)

	// Create form fields for each parameter
	var formGroups []*huh.Group
	var currentFields []huh.Field

	for _, p := range tmpl.Spec.Parameters {
		// Create a string pointer to hold the value (including hidden params)
		defaultVal := ""
		if p.Default != nil {
			defaultVal = fmt.Sprintf("%v", p.Default)
		}
		paramValues[p.Name] = &defaultVal

		// Skip hidden parameters - they use their default value
		if p.Hidden {
			continue
		}

		field := createField(p, paramValues[p.Name])
		if field != nil {
			currentFields = append(currentFields, field)
		}

		// Group fields (max 5 per group for better UX)
		if len(currentFields) >= 5 {
			formGroups = append(formGroups, huh.NewGroup(currentFields...))
			currentFields = nil
		}
	}

	// Add remaining fields as final group
	if len(currentFields) > 0 {
		formGroups = append(formGroups, huh.NewGroup(currentFields...))
	}

	// Run the parameter form
	if len(formGroups) > 0 {
		paramForm := huh.NewForm(formGroups...)
		if err := paramForm.Run(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Resolve random selections and collect non-empty values
	for _, p := range tmpl.Spec.Parameters {
		strVal := *paramValues[p.Name]
		if strVal == "" {
			continue
		}
		// If user selected random, pick a random enum value
		if strVal == randomMarker && len(p.Enum) > 0 {
			randomIdx := rand.Intn(len(p.Enum))
			strVal = p.Enum[randomIdx]
			fmt.Printf("Random selection for %s: %s\n", p.Name, strVal)
		}
		params[p.Name] = strVal
	}

	// Step 3: Confirm and render (default: Yes)
	confirm := true
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Render the claim?").
				Description("This will call the API to generate YAML").
				Affirmative("Yes, render it").
				Negative("Cancel").
				Value(&confirm),
		),
	)

	if err := confirmForm.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if !confirm {
		fmt.Println("Cancelled.")
		os.Exit(0)
	}

	// Call API to render
	fmt.Println("\nCalling API to render...")

	result, err := renderTemplate(client, apiURL, selectedTemplate, params)
	if err != nil {
		fmt.Println(errorStyle.Render(fmt.Sprintf("Render failed: %v", err)))
		os.Exit(1)
	}

	fmt.Println(successStyle.Render("\nRendered successfully!"))
	fmt.Println(yamlStyle.Render(result))

	// Generate default save path
	resourceName := "output"
	if name, ok := params["name"]; ok {
		resourceName = fmt.Sprintf("%v", name)
	}
	defaultSavePath := fmt.Sprintf("/tmp/%s-%s.yaml", tmpl.Metadata.Name, resourceName)

	// Ask to save (with default path)
	savePath := defaultSavePath
	saveForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Save to file?").
				Description("Press Enter to use default, or clear to skip").
				Value(&savePath),
		),
	)

	if err := saveForm.Run(); err == nil && savePath != "" {
		if err := os.WriteFile(savePath, []byte(result), 0644); err != nil {
			fmt.Printf("Failed to save: %v\n", err)
		} else {
			fmt.Printf("Saved to %s\n", savePath)
		}
	}
}

// fetchTemplates retrieves all templates from the API
func fetchTemplates(client *http.Client, apiURL string) ([]ClaimTemplate, error) {
	resp, err := client.Get(apiURL + "/api/v1/claim-templates")
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var list ClaimTemplateList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return list.Items, nil
}

// renderTemplate calls the API to render a template
func renderTemplate(client *http.Client, apiURL, templateName string, params map[string]interface{}) (string, error) {
	reqBody := OrderRequest{Parameters: params}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/claim-templates/%s/order", apiURL, templateName)
	resp, err := client.Post(url, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var orderResp OrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&orderResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return orderResp.Rendered, nil
}

// createField creates the appropriate huh field based on parameter type
func createField(p Parameter, value *string) huh.Field {
	title := p.Title
	if p.Required {
		title += " *"
	}

	description := p.Description
	if p.Pattern != "" {
		description += fmt.Sprintf(" (pattern: %s)", p.Pattern)
	}

	// If parameter has enum values, use Select
	if len(p.Enum) > 0 {
		var options []huh.Option[string]

		// Add Random option if allowed
		if p.AllowRandom {
			options = append(options, huh.NewOption(randomMarker, randomMarker))
		}

		for _, e := range p.Enum {
			enumStr := fmt.Sprintf("%v", e)
			options = append(options, huh.NewOption(enumStr, enumStr))
		}

		return huh.NewSelect[string]().
			Title(title).
			Description(description).
			Options(options...).
			Value(value)
	}

	// Handle different types
	switch p.Type {
	case "boolean":
		return huh.NewSelect[string]().
			Title(title).
			Description(description).
			Options(
				huh.NewOption("true", "true"),
				huh.NewOption("false", "false"),
			).
			Value(value)

	case "integer":
		return huh.NewInput().
			Title(title).
			Description(description).
			Placeholder(fmt.Sprintf("default: %v", p.Default)).
			Value(value).
			Validate(func(s string) error {
				if s == "" {
					return nil
				}
				if _, err := strconv.Atoi(s); err != nil {
					return fmt.Errorf("must be a number")
				}
				return nil
			})

	default: // string
		return huh.NewInput().
			Title(title).
			Description(description).
			Placeholder(fmt.Sprintf("default: %v", p.Default)).
			Value(value)
	}
}
