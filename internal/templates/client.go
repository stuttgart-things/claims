package templates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
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

// APIError is a non-200 answer of the API. claim-machinery-api answers
// invalid parameters with 400 {"error":"invalid parameters","fields":{…}}
// and a failing KCL assert with 400 {"error":"<assert message>"}
// (claim-machinery-api#135); Message and Fields hold them.
type APIError struct {
	Status  int
	Message string            // the "error" of a JSON body, else the body
	Fields  map[string]string // parameter -> what is wrong (400)
	Hint    string            // 401/403: which auth settings to check
}

// Error renders the error readably: one line per field error, the
// message of a JSON body without ANSI codes and escapes.
func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "API returned %d: %s", e.Status, e.Message)
	for _, name := range e.FieldNames() {
		fmt.Fprintf(&b, "\n  %s: %s", name, e.Fields[name])
	}
	if e.Hint != "" {
		b.WriteString("\n" + e.Hint)
	}
	return b.String()
}

// FieldNames returns the names of Fields, sorted.
func (e *APIError) FieldNames() []string {
	names := make([]string, 0, len(e.Fields))
	for n := range e.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ansiRE matches ANSI escape sequences (older APIs pass KCL's colored
// stderr through).
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// statusError builds the error for a non-200 response. 401/403 responses get
// a hint naming the auth settings (CLAIM_API_TOKEN / CLAIM_API_OIDC_*) and
// keep the body as it is.
func statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	e := &APIError{
		Status:  resp.StatusCode,
		Message: string(bytes.TrimSpace(body)),
		Hint:    apiauth.StatusHint(resp.StatusCode),
	}
	if e.Hint != "" {
		return e
	}
	var parsed struct {
		Error  string            `json:"error"`
		Fields map[string]string `json:"fields"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error != "" {
		msg := ansiRE.ReplaceAllString(parsed.Error, "")
		lines := strings.Split(msg, "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t")
		}
		e.Message = strings.TrimSpace(strings.Join(lines, "\n"))
		e.Fields = parsed.Fields
	}
	return e
}
