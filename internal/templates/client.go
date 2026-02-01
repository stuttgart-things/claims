package templates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is the API client for claim templates
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient creates a new template API client
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewClientWithHTTPClient creates a new template API client with a custom HTTP client
func NewClientWithHTTPClient(baseURL string, httpClient *http.Client) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: httpClient,
	}
}

// FetchTemplates retrieves all templates from the API
func (c *Client) FetchTemplates() ([]ClaimTemplate, error) {
	resp, err := c.HTTPClient.Get(c.BaseURL + "/api/v1/claim-templates")
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

// RenderTemplate calls the API to render a template with the given parameters
func (c *Client) RenderTemplate(templateName string, params map[string]interface{}) (string, error) {
	reqBody := OrderRequest{Parameters: params}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/claim-templates/%s/order", c.BaseURL, templateName)
	resp, err := c.HTTPClient.Post(url, "application/json", bytes.NewReader(jsonData))
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
