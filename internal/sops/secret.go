package sops

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// SecretData holds the data needed to generate a Kubernetes Secret YAML.
type SecretData struct {
	Name       string
	Namespace  string
	StringData map[string]string
}

// GenerateSecretYAML produces a Kubernetes Secret manifest in YAML format.
func GenerateSecretYAML(data SecretData) ([]byte, error) {
	if data.Name == "" {
		return nil, fmt.Errorf("secret name is required")
	}
	if data.Namespace == "" {
		return nil, fmt.Errorf("secret namespace is required")
	}

	secret := map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      data.Name,
			"namespace": data.Namespace,
		},
		"type":       "Opaque",
		"stringData": data.StringData,
	}

	out, err := yaml.Marshal(secret)
	if err != nil {
		return nil, fmt.Errorf("marshalling secret YAML: %w", err)
	}

	return out, nil
}
