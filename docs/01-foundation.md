# Phase 1: Foundation (Refactoring)

## Goal
Prepare the codebase for extension without breaking existing functionality.

---

## Tasks

- [ ] Extract types to `internal/templates/types.go`
- [ ] Extract API client to `internal/templates/client.go`
- [ ] Create `cmd/render_types.go` with RenderConfig/RenderResult
- [ ] Move interactive logic to `cmd/render_interactive.go`
- [ ] Verify existing behavior unchanged

---

## Details

### 1. Extract Types (`internal/templates/types.go`)

Move from `cmd/render.go`:
```go
package templates

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
```

### 2. Extract API Client (`internal/templates/client.go`)

```go
package templates

type Client struct {
    BaseURL    string
    HTTPClient *http.Client
}

func NewClient(baseURL string) *Client {
    return &Client{
        BaseURL:    baseURL,
        HTTPClient: &http.Client{Timeout: 30 * time.Second},
    }
}

func (c *Client) FetchTemplates() ([]ClaimTemplate, error) {
    // Move fetchTemplates() logic here
}

func (c *Client) RenderTemplate(name string, params map[string]interface{}) (string, error) {
    // Move renderTemplate() logic here
}
```

### 3. Create Shared Types (`cmd/render_types.go`)

```go
package cmd

type RenderConfig struct {
    APIUrl          string
    Templates       []string
    ParamsFile      string
    InlineParams    map[string]string
    OutputDir       string
    FilenamePattern string
    SingleFile      bool
    DryRun          bool
    Interactive     bool
    GitConfig       *GitConfig
    PRConfig        *PRConfig
}

type GitConfig struct {
    Commit       bool
    Push         bool
    CreateBranch bool
    Message      string
    Branch       string
    Remote       string
    RepoURL      string
    User         string
    Token        string
}

type PRConfig struct {
    Create      bool
    Title       string
    Description string
    Labels      []string
    BaseBranch  string
}

type RenderResult struct {
    TemplateName string
    ResourceName string
    OutputPath   string
    Content      string
    Params       map[string]interface{}
    Error        error
}
```

### 4. Split render.go

Keep in `cmd/render.go`:
- Flag definitions
- `init()` function
- `runRender()` as entry point that routes to interactive/non-interactive

Move to `cmd/render_interactive.go`:
- All huh form logic
- `createField()` function
- Styles (or move to separate `cmd/styles.go`)

---

## Verification

```bash
# Existing behavior must work unchanged
claims render
# Should: show logo, prompt API URL, select template, fill params, render
```

---

## Files to Modify/Create

| File | Action |
|------|--------|
| `cmd/render.go` | Slim down, keep flags + routing |
| `cmd/render_interactive.go` | Create, move form logic |
| `cmd/render_types.go` | Create |
| `internal/templates/types.go` | Create |
| `internal/templates/client.go` | Create |
