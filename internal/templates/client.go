package templates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/stuttgart-things/claims/internal/apiauth"
)

const defaultTimeout = 30 * time.Second

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
			Timeout: defaultTimeout,
		},
	}
}

// NewClientWithAuth creates a template API client that sends the bearer token
// from auth on every request. A nil auth behaves like NewClient (no header).
func NewClientWithAuth(baseURL string, auth *apiauth.Authenticator) *Client {
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: auth.HTTPClient(defaultTimeout),
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", statusError(resp)
	}

	var orderResp OrderResponse
	if err := json.NewDecoder(resp.Body).Decode(&orderResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return orderResp.Rendered, nil
}

// statusError builds the error for a non-200 response. 401/403 responses get
// a hint naming the auth settings (CLAIM_API_TOKEN / CLAIM_API_OIDC_*).
func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := fmt.Sprintf("API returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	if hint := apiauth.StatusHint(resp.StatusCode); hint != "" {
		return fmt.Errorf("%s\n%s", msg, hint)
	}
	return errors.New(msg)
}
