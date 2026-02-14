package sops

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// CheckSOPSInstalled returns true if the sops binary is on PATH.
func CheckSOPSInstalled() bool {
	_, err := exec.LookPath("sops")
	return err == nil
}

// CheckSOPSAvailable verifies that the sops binary is installed and
// the SOPS_AGE_RECIPIENTS environment variable is set.
// It returns the recipients string on success.
func CheckSOPSAvailable() (string, error) {
	if !CheckSOPSInstalled() {
		return "", fmt.Errorf("sops CLI not found: install from https://github.com/getsops/sops")
	}

	recipients := os.Getenv("SOPS_AGE_RECIPIENTS")
	if recipients == "" {
		return "", fmt.Errorf("SOPS_AGE_RECIPIENTS environment variable is not set")
	}

	return recipients, nil
}

// Encrypt encrypts plaintext YAML using sops with age encryption.
// It writes the plaintext to a temporary file, runs sops --encrypt, and
// returns the encrypted output.
func Encrypt(plaintext []byte, recipients string) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "claims-secret-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(plaintext); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("writing temp file: %w", err)
	}
	tmpFile.Close()

	cmd := exec.Command("sops",
		"--encrypt",
		"--age", recipients,
		"--input-type", "yaml",
		"--output-type", "yaml",
		tmpFile.Name(),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		return nil, fmt.Errorf("sops encrypt failed: %s", errMsg)
	}

	return stdout.Bytes(), nil
}
