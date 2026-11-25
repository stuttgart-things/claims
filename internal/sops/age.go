package sops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// AgeKeys describes the age identities sops would use to decrypt.
type AgeKeys struct {
	Source     string   // where they come from (an env var or a file), never the key
	Recipients []string // the public keys of the identities ("" when unknown)
	Opaque     bool     // SOPS_AGE_KEY_CMD: the keys cannot be inspected
}

// defaultAgeKeyFile is where sops looks without SOPS_AGE_KEY*:
// $XDG_CONFIG_HOME/sops/age/keys.txt, else <user config dir>/sops/age/keys.txt.
func defaultAgeKeyFile() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "sops", "age", "keys.txt")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "sops", "age", "keys.txt")
}

// FindAgeKeys looks for the age identities the way sops does: SOPS_AGE_KEY,
// SOPS_AGE_KEY_FILE, SOPS_AGE_KEY_CMD, then the default key file. It returns
// their public keys so callers can check them against the recipients of a
// rule; the private keys stay in this function.
func FindAgeKeys() (*AgeKeys, error) {
	if k := os.Getenv("SOPS_AGE_KEY"); k != "" {
		return parseAgeKeys("$SOPS_AGE_KEY", k)
	}
	if f := os.Getenv("SOPS_AGE_KEY_FILE"); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("$SOPS_AGE_KEY_FILE (%s): %w", f, err)
		}
		return parseAgeKeys(f, string(data))
	}
	if os.Getenv("SOPS_AGE_KEY_CMD") != "" {
		return &AgeKeys{Source: "$SOPS_AGE_KEY_CMD", Opaque: true}, nil
	}
	f := defaultAgeKeyFile()
	data, err := os.ReadFile(f)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no age key: set SOPS_AGE_KEY_FILE (or SOPS_AGE_KEY / SOPS_AGE_KEY_CMD), or put the key into %s", f)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f, err)
	}
	return parseAgeKeys(f, string(data))
}

func parseAgeKeys(source, data string) (*AgeKeys, error) {
	ids, err := age.ParseIdentities(strings.NewReader(data))
	if err != nil {
		// The parser error may quote the input line: keep it out.
		return nil, fmt.Errorf("%s holds no valid age identity", source)
	}
	k := &AgeKeys{Source: source}
	for _, id := range ids {
		if x, ok := id.(*age.X25519Identity); ok {
			k.Recipients = append(k.Recipients, x.Recipient().String())
		}
	}
	return k, nil
}

// CanDecrypt reports whether one of the keys is among recipients; unknown
// (true, false) for SOPS_AGE_KEY_CMD.
func (k *AgeKeys) CanDecrypt(recipients []string) (ok, known bool) {
	if k.Opaque {
		return false, false
	}
	for _, r := range k.Recipients {
		for _, want := range recipients {
			if r == want {
				return true, true
			}
		}
	}
	return false, true
}
