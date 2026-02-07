package kustomize

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Kustomization represents a kustomization.yaml file
type Kustomization struct {
	APIVersion string   `yaml:"apiVersion,omitempty"`
	Kind       string   `yaml:"kind,omitempty"`
	Resources  []string `yaml:"resources"`
}

// Load reads and parses a kustomization.yaml file
func Load(path string) (*Kustomization, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading kustomization file: %w", err)
	}

	var k Kustomization
	if err := yaml.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("parsing kustomization file: %w", err)
	}

	return &k, nil
}

// Save writes a Kustomization to a YAML file
func Save(path string, k *Kustomization) error {
	data, err := yaml.Marshal(k)
	if err != nil {
		return fmt.Errorf("marshalling kustomization: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing kustomization file: %w", err)
	}

	return nil
}

// AddResource adds a resource entry if it doesn't already exist
func AddResource(k *Kustomization, resource string) {
	for _, r := range k.Resources {
		if r == resource {
			return
		}
	}
	k.Resources = append(k.Resources, resource)
}

// RemoveResource removes a resource entry by value.
// Returns an error if the resource is not found.
func RemoveResource(k *Kustomization, resource string) error {
	for i, r := range k.Resources {
		if r == resource {
			k.Resources = append(k.Resources[:i], k.Resources[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("resource %q not found in kustomization", resource)
}
