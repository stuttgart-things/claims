package templates

// ClaimTemplate represents a claim template from the API
type ClaimTemplate struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   ClaimTemplateMetadata `json:"metadata"`
	Spec       ClaimTemplateSpec     `json:"spec"`
}

// ClaimTemplateMetadata contains template metadata
type ClaimTemplateMetadata struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// ClaimTemplateSpec contains template specification
type ClaimTemplateSpec struct {
	Type       string           `json:"type"`
	Source     string           `json:"source"`
	Tag        string           `json:"tag,omitempty"`
	Parameters []Parameter      `json:"parameters"`
	Secrets    []SecretTemplate `json:"secrets,omitempty"`
}

// SecretTemplate defines a Kubernetes Secret that accompanies the rendered template.
// Secret values are collected and encrypted client-side (SOPS/age) — they never pass through the API.
type SecretTemplate struct {
	Name       string      `json:"name"`       // supports Go template expressions e.g. "{{.secretName}}"
	Namespace  string      `json:"namespace"`   // supports Go template expressions
	Parameters []Parameter `json:"parameters"` // secret key definitions
}

// ValueFromSpec references a profile function to dynamically resolve a parameter value.
type ValueFromSpec struct {
	Function  string            `json:"function"`
	Args      map[string]string `json:"args"`
	Condition string            `json:"condition,omitempty"`
}

// Parameter defines a template parameter
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
	Multiselect bool        `json:"multiselect,omitempty"`
	ValueFrom   *ValueFromSpec `json:"valueFrom,omitempty"`
}

// ClaimTemplateList is a list of claim templates
type ClaimTemplateList struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Items      []ClaimTemplate `json:"items"`
}

// OrderRequest is the request body for rendering a template
type OrderRequest struct {
	Parameters map[string]interface{} `json:"parameters"`
}

// OrderResponse is the response from rendering a template
type OrderResponse struct {
	APIVersion string                 `json:"apiVersion"`
	Kind       string                 `json:"kind"`
	Metadata   map[string]interface{} `json:"metadata"`
	Rendered   string                 `json:"rendered"`
}
