// Package endpoint keeps the named claim-machinery-api endpoints of
// ~/.config/claims/config.yaml (claims#116), like the contexts of a
// kubeconfig: one per lab, each with its URL and the ZITADEL `claims login`
// uses for it. Nothing is pre-seeded: the file holds what the user added.
package endpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EnvConfigFile overrides where the config file lives.
const EnvConfigFile = "CLAIMS_CONFIG_FILE"

// EnvEndpoint names the endpoint to use (like --endpoint).
const EnvEndpoint = "CLAIM_ENDPOINT"

// Endpoint is one claim-machinery-api.
type Endpoint struct {
	URL      string `yaml:"url"`
	Issuer   string `yaml:"issuer,omitempty"`    // OIDC issuer for `claims login`
	ClientID string `yaml:"client-id,omitempty"` // native app of the issuer
	CAFile   string `yaml:"ca-file,omitempty"`   // extra PEM roots for the issuer
}

// Config is the config file.
type Config struct {
	Current   string              `yaml:"current,omitempty"`
	Endpoints map[string]Endpoint `yaml:"endpoints,omitempty"`
}

// namePattern keeps names usable as flag values and file keys.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// DefaultPath is $CLAIMS_CONFIG_FILE, else <user config dir>/claims/config.yaml.
func DefaultPath() (string, error) {
	if p := os.Getenv(EnvConfigFile); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating the config directory: %w", err)
	}
	return filepath.Join(dir, "claims", "config.yaml"), nil
}

// Load reads path; a missing file is an empty config.
func Load(path string) (*Config, error) {
	c := &Config{Endpoints: map[string]Endpoint{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if c.Endpoints == nil {
		c.Endpoints = map[string]Endpoint{}
	}
	return c, nil
}

// Save writes c to path (directory 0700, file 0600).
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// NormalizeURL makes URLs comparable (no surrounding space or trailing slash).
func NormalizeURL(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

// Add adds or replaces the endpoint name.
func (c *Config) Add(name string, e Endpoint) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("endpoint name %q: use letters, digits, '.', '_' and '-'", name)
	}
	e.URL = NormalizeURL(e.URL)
	if !strings.HasPrefix(e.URL, "http://") && !strings.HasPrefix(e.URL, "https://") {
		return fmt.Errorf("endpoint URL %q must start with http:// or https://", e.URL)
	}
	if (e.Issuer == "") != (e.ClientID == "") {
		return errors.New("--issuer and --client-id go together (claims login needs both)")
	}
	c.Endpoints[name] = e
	return nil
}

// Remove deletes name; removing the current endpoint unsets current.
func (c *Config) Remove(name string) error {
	if _, ok := c.Endpoints[name]; !ok {
		return c.unknown(name)
	}
	delete(c.Endpoints, name)
	if c.Current == name {
		c.Current = ""
	}
	return nil
}

// Use makes name the current endpoint.
func (c *Config) Use(name string) error {
	if _, ok := c.Endpoints[name]; !ok {
		return c.unknown(name)
	}
	c.Current = name
	return nil
}

// Get returns the endpoint name.
func (c *Config) Get(name string) (Endpoint, error) {
	e, ok := c.Endpoints[name]
	if !ok {
		return Endpoint{}, c.unknown(name)
	}
	return e, nil
}

// ByURL returns the name of the endpoint with url, if any.
func (c *Config) ByURL(url string) (string, Endpoint, bool) {
	url = NormalizeURL(url)
	for _, n := range c.Names() {
		if e := c.Endpoints[n]; e.URL == url {
			return n, e, true
		}
	}
	return "", Endpoint{}, false
}

// Names lists the endpoint names, sorted.
func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Endpoints))
	for n := range c.Endpoints {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (c *Config) unknown(name string) error {
	if len(c.Endpoints) == 0 {
		return fmt.Errorf("unknown endpoint %q: none configured (add one with `claims endpoint add <name> <url>`)", name)
	}
	return fmt.Errorf("unknown endpoint %q (configured: %s)", name, strings.Join(c.Names(), ", "))
}
