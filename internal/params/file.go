package params

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseFile reads and parses a parameter file (YAML or JSON)
func ParseFile(path string) (*ParameterFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading params file: %w", err)
	}

	var pf ParameterFile

	// Detect format by extension or try both
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &pf); err != nil {
			return nil, fmt.Errorf("parsing JSON: %w", err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &pf); err != nil {
			return nil, fmt.Errorf("parsing YAML: %w", err)
		}
	default:
		// Try YAML first, then JSON
		if err := yaml.Unmarshal(data, &pf); err != nil {
			if jsonErr := json.Unmarshal(data, &pf); jsonErr != nil {
				return nil, fmt.Errorf("parsing params file (tried YAML and JSON): %w", err)
			}
		}
	}

	pf.Normalize()
	return &pf, nil
}

// ParseInlineParams parses key=value strings into a map
func ParseInlineParams(params []string) (map[string]any, error) {
	result := make(map[string]any)

	for _, p := range params {
		parts := strings.SplitN(p, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid param format: %s (expected key=value)", p)
		}
		result[parts[0]] = parts[1]
	}

	return result, nil
}

// MergeParams merges file params with inline params (inline takes precedence)
func MergeParams(fileParams, inlineParams map[string]any) map[string]any {
	result := make(map[string]any)

	for k, v := range fileParams {
		result[k] = v
	}
	for k, v := range inlineParams {
		result[k] = v
	}

	return result
}
