package sops

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Annotations claims sets on the Secrets below secrets/ (plain metadata, not
// encrypted: encrypted_regex covers data/stringData only).
const (
	AnnotationLab       = "claims.stuttgart-things.com/lab"
	AnnotationVaultPath = "claims.stuttgart-things.com/vault-path"
)

// Manifest is a Kubernetes Secret as claims secret reads and writes it.
// Values are kept as strings; data (base64) is decoded on read and written
// back as stringData, the canonical form of files below secrets/.
type Manifest struct {
	Metadata map[string]any
	Type     string
	Values   map[string]string
}

var keyPattern = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)

// ValidKey reports whether k may be a key of a Secret.
func ValidKey(k string) error {
	if len(k) > 253 || !keyPattern.MatchString(k) {
		return fmt.Errorf("key %q: a Secret key consists of letters, digits, '-', '_' and '.'", k)
	}
	return nil
}

// NewManifest returns an Opaque Secret name/namespace with annotations.
func NewManifest(name, namespace string, annotations map[string]string) *Manifest {
	md := map[string]any{"name": name, "namespace": namespace}
	if len(annotations) > 0 {
		a := map[string]any{}
		for k, v := range annotations {
			a[k] = v
		}
		md["annotations"] = a
	}
	return &Manifest{Metadata: md, Type: "Opaque", Values: map[string]string{}}
}

// ParseManifest parses decrypted YAML of a Secret.
func ParseManifest(plaintext []byte) (*Manifest, error) {
	var doc struct {
		APIVersion string            `yaml:"apiVersion"`
		Kind       string            `yaml:"kind"`
		Metadata   map[string]any    `yaml:"metadata"`
		Type       string            `yaml:"type"`
		Data       map[string]string `yaml:"data"`
		StringData map[string]string `yaml:"stringData"`
	}
	if err := yaml.Unmarshal(plaintext, &doc); err != nil {
		// yaml errors quote input lines: never pass them on.
		return nil, fmt.Errorf("the decrypted file is not a Kubernetes Secret manifest (YAML error hidden, it could quote a value)")
	}
	if doc.Kind != "Secret" {
		return nil, fmt.Errorf("the decrypted file is kind %q, expected a Kubernetes Secret", doc.Kind)
	}
	m := &Manifest{Metadata: doc.Metadata, Type: doc.Type, Values: map[string]string{}}
	if m.Metadata == nil {
		m.Metadata = map[string]any{}
	}
	for k, v := range doc.Data {
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("data.%s is not valid base64", k)
		}
		m.Values[k] = string(b)
	}
	for k, v := range doc.StringData { // stringData wins, as in the API server
		m.Values[k] = v
	}
	return m, nil
}

// Name is metadata.name.
func (m *Manifest) Name() string { s, _ := m.Metadata["name"].(string); return s }

// Namespace is metadata.namespace.
func (m *Manifest) Namespace() string { s, _ := m.Metadata["namespace"].(string); return s }

// Marshal renders the Secret with all values as stringData.
func (m *Manifest) Marshal() ([]byte, error) {
	t := m.Type
	if t == "" {
		t = "Opaque"
	}
	doc := struct {
		APIVersion string            `yaml:"apiVersion"`
		Kind       string            `yaml:"kind"`
		Metadata   map[string]any    `yaml:"metadata"`
		Type       string            `yaml:"type"`
		StringData map[string]string `yaml:"stringData"`
	}{"v1", "Secret", m.Metadata, t, m.Values}
	if doc.StringData == nil {
		doc.StringData = map[string]string{}
	}
	return yaml.Marshal(doc)
}

// SecretValues lists the values (for redaction).
func (m *Manifest) SecretValues() []string {
	out := make([]string, 0, len(m.Values))
	for _, v := range m.Values {
		out = append(out, v)
	}
	return out
}

// Keys returns the sorted keys of values.
func Keys(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for k := range values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Hash is the hex sha256 of v.
func Hash(v string) string {
	s := sha256.Sum256([]byte(v))
	return hex.EncodeToString(s[:])
}

// HashPrefixLen is how much of a hash claims prints.
const HashPrefixLen = 12

// ShortHash is the printed prefix of Hash(v).
func ShortHash(v string) string { return Hash(v)[:HashPrefixLen] }

// SameValues reports whether a and b hold the same keys with the same values.
func SameValues(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// CheckEncrypted verifies that every value under data and stringData of
// ciphertext is a sops ENC[...] value, so a .sops.yaml whose
// encrypted_regex misses them can never put plaintext into git.
func CheckEncrypted(ciphertext []byte) error {
	var doc struct {
		Data       map[string]any `yaml:"data"`
		StringData map[string]any `yaml:"stringData"`
		Sops       map[string]any `yaml:"sops"`
	}
	if err := yaml.Unmarshal(ciphertext, &doc); err != nil || doc.Sops == nil {
		return fmt.Errorf("sops output is not an encrypted YAML document")
	}
	for field, m := range map[string]map[string]any{"data": doc.Data, "stringData": doc.StringData} {
		for k, v := range m {
			s, _ := v.(string)
			if !strings.HasPrefix(s, "ENC[") {
				return fmt.Errorf("%s.%s is not encrypted", field, k)
			}
		}
	}
	return nil
}
