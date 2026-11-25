package cmd

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Generated values (claims secret sops set --generate) come into existence
// in the SOPS file, so a declared secret that is generated is never lost
// with Vault (crossplane-configurations#572).

// secretCharsets are the named character sets of --charset.
var secretCharsets = map[string]string{
	"alnum":       "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
	"alpha":       "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
	"lower-alnum": "abcdefghijklmnopqrstuvwxyz0123456789",
	"hex":         "0123456789abcdef",
	"base64url":   "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_",
	// printable ASCII without space, quotes, backslash and backtick: safe in
	// YAML, shells and URLs' userinfo after escaping.
	"symbols": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!#$%&()*+,-./:;<=>?@[]^_{|}~",
}

const (
	minGeneratedLength = 8
	maxGeneratedLength = 4096
)

// charsetNames lists the named sets for help texts.
func charsetNames() string {
	names := make([]string, 0, len(secretCharsets))
	for n := range secretCharsets {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// resolveCharset is a named set or "custom:<characters>".
func resolveCharset(name string) (string, error) {
	if cs, ok := strings.CutPrefix(name, "custom:"); ok {
		seen := map[rune]bool{}
		var b strings.Builder
		for _, r := range cs {
			if r < 0x21 || r > 0x7e {
				return "", usagef("--charset custom: printable ASCII without space only")
			}
			if !seen[r] {
				seen[r] = true
				b.WriteRune(r)
			}
		}
		if b.Len() < 2 {
			return "", usagef("--charset custom: needs at least 2 distinct characters")
		}
		return b.String(), nil
	}
	cs, ok := secretCharsets[name]
	if !ok {
		return "", usagef("--charset %q: use one of %s, or custom:<characters>", name, charsetNames())
	}
	return cs, nil
}

// generateValue returns length characters of charset from crypto/rand
// (uniform: rand.Int has no modulo bias).
func generateValue(length int, charset string) (string, error) {
	if length < minGeneratedLength || length > maxGeneratedLength {
		return "", usagef("--length %d: use %d to %d", length, minGeneratedLength, maxGeneratedLength)
	}
	max := big.NewInt(int64(len(charset)))
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generating a value: %w", err)
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}
