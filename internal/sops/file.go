package sops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file is the SOPS side of `claims secret` (claims#122): files below
// secrets/ of the fleet repository, encrypted for the recipients the
// repository's .sops.yaml names for their path. Plaintext only ever lives in
// memory: sops decrypts to a pipe and encrypts from stdin, with
// --filename-override so the creation_rules of .sops.yaml apply.

// Binary is the sops executable; a variable for tests.
var Binary = "sops"

// MinVersion is the oldest sops claims secret works with (--filename-override).
var MinVersion = [3]int{3, 9, 0}

// Version returns the version of the sops binary, e.g. "3.10.2".
func Version(ctx context.Context) (string, error) {
	if _, err := exec.LookPath(Binary); err != nil {
		return "", fmt.Errorf("sops not found on PATH: install it from https://github.com/getsops/sops/releases")
	}
	// --disable-version-check keeps sops off the network (3.10+); older
	// versions do not know the flag and never checked.
	out, err := exec.CommandContext(ctx, Binary, "--disable-version-check", "--version").Output()
	if err != nil {
		out, err = exec.CommandContext(ctx, Binary, "--version").Output()
		if err != nil {
			return "", fmt.Errorf("sops --version: %w", err)
		}
	}
	m := regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`).FindString(string(out))
	if m == "" {
		return "", fmt.Errorf("sops --version: unexpected output %q", firstLine(string(out)))
	}
	return m, nil
}

// VersionAtLeast reports whether v (x.y.z) is at least min.
func VersionAtLeast(v string, min [3]int) bool {
	parts := strings.SplitN(v, ".", 3)
	for i := 0; i < 3; i++ {
		n := 0
		if i < len(parts) {
			n, _ = strconv.Atoi(parts[i])
		}
		if n != min[i] {
			return n > min[i]
		}
	}
	return true
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// run executes sops in dir with stdin and returns stdout. stderr goes into
// the error, with every string of redact removed (belt and braces: sops does
// not print values, but its error text must never carry one).
func run(ctx context.Context, dir string, stdin []byte, redact []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, Binary, args...)
	cmd.Dir = dir
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		for _, v := range redact {
			if len(v) >= 4 {
				msg = strings.ReplaceAll(msg, v, "<redacted>")
			}
		}
		return nil, errors.New(msg)
	}
	return stdout.Bytes(), nil
}

// Decrypt decrypts rel (relative to repoRoot) into memory.
func Decrypt(ctx context.Context, repoRoot, rel string) ([]byte, error) {
	out, err := run(ctx, repoRoot, nil, nil, "--decrypt", filepath.FromSlash(rel))
	if err != nil {
		return nil, fmt.Errorf("sops --decrypt %s: %w", rel, err)
	}
	return out, nil
}

// EncryptFor encrypts plaintext (YAML) for rel: sops reads it from stdin and
// takes the recipients from the creation rule of repoRoot's .sops.yaml that
// matches rel. redact lists the values in plaintext (kept out of errors).
func EncryptFor(ctx context.Context, repoRoot, rel string, plaintext []byte, redact []string) ([]byte, error) {
	out, err := run(ctx, repoRoot, plaintext, redact,
		"--encrypt",
		"--filename-override", filepath.FromSlash(rel),
		"--input-type", "yaml", "--output-type", "yaml",
		"/dev/stdin")
	if err != nil {
		return nil, fmt.Errorf("sops --encrypt for %s: %w", rel, err)
	}
	return out, nil
}

// UpdateKeys re-wraps the data key of rel for the recipients .sops.yaml
// names now (sops updatekeys); it needs a key that can decrypt rel.
func UpdateKeys(ctx context.Context, repoRoot, rel string) error {
	if _, err := run(ctx, repoRoot, nil, nil, "updatekeys", "--yes", filepath.FromSlash(rel)); err != nil {
		return fmt.Errorf("sops updatekeys %s: %w", rel, err)
	}
	return nil
}

// Rotate generates a new data key for rel and re-encrypts it in place
// (sops rotate --in-place); the file never exists as plaintext.
func Rotate(ctx context.Context, repoRoot, rel string) error {
	if _, err := run(ctx, repoRoot, nil, nil, "rotate", "--in-place", filepath.FromSlash(rel)); err != nil {
		return fmt.Errorf("sops rotate %s: %w", rel, err)
	}
	return nil
}

// WriteFileAtomic writes data (ciphertext) to path via a temporary file in
// the same directory and a rename.
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".claims-*.enc.yaml")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// FileRecipients returns the age recipients in the sops metadata of an
// encrypted file (public keys, safe to print).
func FileRecipients(encrypted []byte) []string {
	var doc struct {
		Sops struct {
			Age []struct {
				Recipient string `yaml:"recipient"`
			} `yaml:"age"`
		} `yaml:"sops"`
	}
	if yaml.Unmarshal(encrypted, &doc) != nil {
		return nil
	}
	var out []string
	for _, a := range doc.Sops.Age {
		out = append(out, a.Recipient)
	}
	return out
}

// IsEncrypted reports whether data carries sops metadata.
func IsEncrypted(data []byte) bool {
	var doc map[string]any
	if yaml.Unmarshal(data, &doc) != nil {
		return false
	}
	_, ok := doc["sops"]
	return ok
}
