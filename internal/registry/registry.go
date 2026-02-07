package registry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

const (
	DefaultAPIVersion = "claim-registry.io/v1alpha1"
	DefaultKind       = "ClaimRegistry"
)

// Load reads and parses a registry.yaml file
func Load(path string) (*ClaimRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading registry file: %w", err)
	}

	var reg ClaimRegistry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parsing registry file: %w", err)
	}

	return &reg, nil
}

// Save writes a ClaimRegistry to a YAML file
func Save(path string, reg *ClaimRegistry) error {
	if reg.APIVersion == "" {
		reg.APIVersion = DefaultAPIVersion
	}
	if reg.Kind == "" {
		reg.Kind = DefaultKind
	}

	data, err := yaml.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshalling registry: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing registry file: %w", err)
	}

	return nil
}

// AddEntry adds a claim entry to the registry.
// If an entry with the same name already exists, it is replaced.
func AddEntry(reg *ClaimRegistry, entry ClaimEntry) {
	for i, e := range reg.Claims {
		if e.Name == entry.Name {
			reg.Claims[i] = entry
			return
		}
	}
	reg.Claims = append(reg.Claims, entry)
}

// RemoveEntry removes a claim entry by name.
// Returns an error if the entry is not found.
func RemoveEntry(reg *ClaimRegistry, name string) error {
	for i, e := range reg.Claims {
		if e.Name == name {
			reg.Claims = append(reg.Claims[:i], reg.Claims[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("claim %q not found in registry", name)
}

// FindEntry returns a pointer to the claim entry with the given name, or nil.
func FindEntry(reg *ClaimRegistry, name string) *ClaimEntry {
	for i, e := range reg.Claims {
		if e.Name == name {
			return &reg.Claims[i]
		}
	}
	return nil
}

// FilterEntries returns entries matching the given category and/or template.
// Empty strings are treated as wildcards.
func FilterEntries(reg *ClaimRegistry, category, template string) []ClaimEntry {
	var result []ClaimEntry
	for _, e := range reg.Claims {
		if category != "" && e.Category != category {
			continue
		}
		if template != "" && e.Template != template {
			continue
		}
		result = append(result, e)
	}
	return result
}

// NewRegistry creates an empty ClaimRegistry with default fields.
func NewRegistry() *ClaimRegistry {
	return &ClaimRegistry{
		APIVersion: DefaultAPIVersion,
		Kind:       DefaultKind,
		Claims:     []ClaimEntry{},
	}
}
