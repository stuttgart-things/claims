package params

// ParameterFile supports both single and multi-template formats
type ParameterFile struct {
	// Single template format
	Template   string         `yaml:"template" json:"template"`
	Parameters map[string]any `yaml:"parameters" json:"parameters"`

	// Multi-template format
	Templates []TemplateParams `yaml:"templates" json:"templates"`

	// Secret values (kept separate from parameters for clarity)
	Secrets map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// TemplateParams holds parameters for a single template
type TemplateParams struct {
	Name       string            `yaml:"name" json:"name"`
	Parameters map[string]any    `yaml:"parameters" json:"parameters"`
	Secrets    map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// Normalize converts single-template format to multi-template format
func (pf *ParameterFile) Normalize() {
	if pf.Template != "" && len(pf.Templates) == 0 {
		pf.Templates = []TemplateParams{{
			Name:       pf.Template,
			Parameters: pf.Parameters,
			Secrets:    pf.Secrets,
		}}
	}
	// Propagate top-level secrets to templates that don't have their own
	if len(pf.Secrets) > 0 {
		for i := range pf.Templates {
			if pf.Templates[i].Secrets == nil {
				pf.Templates[i].Secrets = pf.Secrets
			}
		}
	}
}
