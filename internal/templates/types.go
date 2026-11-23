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
	Profile     string   `json:"profile,omitempty"`
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
	Namespace  string      `json:"namespace"`  // supports Go template expressions
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
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Type        string         `json:"type"`
	Default     interface{}    `json:"default,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Enum        []string       `json:"enum,omitempty"`
	Pattern     string         `json:"pattern,omitempty"`
	MinLength   *int           `json:"minLength,omitempty"`
	MaxLength   *int           `json:"maxLength,omitempty"`
	Hidden      bool           `json:"hidden,omitempty"`
	AllowRandom bool           `json:"allowRandom,omitempty"`
	Multiselect bool           `json:"multiselect,omitempty"`
	ValueFrom   *ValueFromSpec `json:"valueFrom,omitempty"`

	// ShowWhen makes the parameter conditional (claim-machinery-api#135):
	// it applies only when every named parameter has one of the listed
	// values, e.g. {use_case: [dev]}. See internal/conditions.
	ShowWhen map[string][]string `json:"showWhen,omitempty"`

	// EnumWhen narrows the allowed values by another parameter's value,
	// e.g. {use_case: {dev: [large, xlarge]}}; a value not listed leaves the
	// enum as it is. See internal/conditions.
	EnumWhen map[string]map[string][]string `json:"enumWhen,omitempty"`

	// Identity binds the parameter to the orderer when the API has order
	// auth on (claim-machinery-api#122): "user" or "owner".
	Identity string `json:"identity,omitempty"`

	// AllowedOwners and AnyOwner are what the caller may set an "owner"
	// parameter to: its user ref and groups, and whether any value goes
	// (admin). The template detail sets them for an authenticated request
	// (claim-machinery-api#147).
	AllowedOwners []string `json:"allowedOwners,omitempty"`
	AnyOwner      bool     `json:"anyOwner,omitempty"`
}

// Whoami is the caller as claim-machinery-api sees it
// (GET /api/v1/whoami, claim-machinery-api#147).
type Whoami struct {
	Name     string   `json:"name"`
	Subject  string   `json:"subject,omitempty"`
	UserRef  string   `json:"userRef,omitempty"`
	Roles    []string `json:"roles"`
	Groups   []string `json:"groups"`
	Admin    bool     `json:"admin"`
	MayOrder bool     `json:"mayOrder"`
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
