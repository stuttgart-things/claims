package registry

// ClaimRegistry represents the claims/registry.yaml file
type ClaimRegistry struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Claims     []ClaimEntry `yaml:"claims"`
}

// ClaimEntry represents a single claim in the registry
type ClaimEntry struct {
	Name       string `yaml:"name"`
	Template   string `yaml:"template"`
	Category   string `yaml:"category"`
	Namespace  string `yaml:"namespace"`
	CreatedAt  string `yaml:"createdAt"`
	CreatedBy  string `yaml:"createdBy"`
	Source     string `yaml:"source"`
	Repository string `yaml:"repository"`
	Path       string `yaml:"path"`
	Status     string `yaml:"status"`
}
