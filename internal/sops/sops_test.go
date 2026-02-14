package sops

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateSecretYAML(t *testing.T) {
	data := SecretData{
		Name:      "my-secret",
		Namespace: "default",
		StringData: map[string]string{
			"username": "admin",
			"password": "s3cret",
		},
	}

	out, err := GenerateSecretYAML(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	yaml := string(out)

	if !strings.Contains(yaml, "apiVersion: v1") {
		t.Error("expected apiVersion: v1")
	}
	if !strings.Contains(yaml, "kind: Secret") {
		t.Error("expected kind: Secret")
	}
	if !strings.Contains(yaml, "name: my-secret") {
		t.Error("expected name: my-secret")
	}
	if !strings.Contains(yaml, "namespace: default") {
		t.Error("expected namespace: default")
	}
	if !strings.Contains(yaml, "type: Opaque") {
		t.Error("expected type: Opaque")
	}
	if !strings.Contains(yaml, "username: admin") {
		t.Error("expected username: admin in stringData")
	}
	if !strings.Contains(yaml, "password: s3cret") {
		t.Error("expected password: s3cret in stringData")
	}
}

func TestGenerateSecretYAML_MissingName(t *testing.T) {
	data := SecretData{
		Namespace:  "default",
		StringData: map[string]string{"key": "val"},
	}

	_, err := GenerateSecretYAML(data)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention name, got: %v", err)
	}
}

func TestGenerateSecretYAML_MissingNamespace(t *testing.T) {
	data := SecretData{
		Name:       "my-secret",
		StringData: map[string]string{"key": "val"},
	}

	_, err := GenerateSecretYAML(data)
	if err == nil {
		t.Fatal("expected error for missing namespace")
	}
	if !strings.Contains(err.Error(), "namespace") {
		t.Errorf("error should mention namespace, got: %v", err)
	}
}

func TestCheckSOPSAvailable_NoRecipients(t *testing.T) {
	// Save and clear the env var
	orig := os.Getenv("SOPS_AGE_RECIPIENTS")
	os.Unsetenv("SOPS_AGE_RECIPIENTS")
	defer func() {
		if orig != "" {
			os.Setenv("SOPS_AGE_RECIPIENTS", orig)
		}
	}()

	_, err := CheckSOPSAvailable()
	if err == nil {
		t.Fatal("expected error when SOPS_AGE_RECIPIENTS is unset")
	}

	// Error could be about sops not installed or about recipients — both are valid
	errStr := err.Error()
	if !strings.Contains(errStr, "SOPS_AGE_RECIPIENTS") && !strings.Contains(errStr, "sops") {
		t.Errorf("error should mention SOPS_AGE_RECIPIENTS or sops, got: %v", err)
	}
}

func TestEncrypt(t *testing.T) {
	if !CheckSOPSInstalled() {
		t.Skip("sops not installed, skipping integration test")
	}

	recipients := os.Getenv("SOPS_AGE_RECIPIENTS")
	if recipients == "" {
		t.Skip("SOPS_AGE_RECIPIENTS not set, skipping integration test")
	}

	plaintext := []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: test\nstringData:\n  key: value\n")

	encrypted, err := Encrypt(plaintext, recipients)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if len(encrypted) == 0 {
		t.Fatal("encrypted output is empty")
	}

	// SOPS-encrypted YAML contains the "sops" metadata key
	if !strings.Contains(string(encrypted), "sops") {
		t.Error("encrypted output should contain sops metadata")
	}
}
