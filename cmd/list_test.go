package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stuttgart-things/claims/internal/registry"
)

func TestPrintTable(t *testing.T) {
	entries := []registry.ClaimEntry{
		{
			Name:      "my-vm",
			Template:  "vsphere-vm",
			Category:  "infra",
			Namespace: "default",
			Status:    "active",
			CreatedBy: "admin",
			Source:    "cli",
		},
		{
			Name:      "my-db",
			Template:  "postgres",
			Category:  "apps",
			Namespace: "production",
			Status:    "active",
			CreatedBy: "dev",
			Source:    "cli",
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printTable(entries)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify header columns
	headers := []string{"NAME", "TEMPLATE", "CATEGORY", "NAMESPACE", "STATUS", "CREATED BY", "SOURCE"}
	for _, h := range headers {
		if !strings.Contains(output, h) {
			t.Errorf("table output should contain header %q", h)
		}
	}

	// Verify data rows
	dataChecks := []string{"my-vm", "vsphere-vm", "infra", "my-db", "postgres", "apps", "production", "admin", "dev"}
	for _, d := range dataChecks {
		if !strings.Contains(output, d) {
			t.Errorf("table output should contain %q", d)
		}
	}
}

func TestPrintJSON(t *testing.T) {
	entries := []registry.ClaimEntry{
		{
			Name:      "my-vm",
			Template:  "vsphere-vm",
			Category:  "infra",
			Namespace: "default",
			Status:    "active",
			CreatedBy: "admin",
			Source:    "cli",
		},
		{
			Name:      "my-db",
			Template:  "postgres",
			Category:  "apps",
			Namespace: "production",
			Status:    "active",
			CreatedBy: "dev",
			Source:    "cli",
		},
	}

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printJSON(entries)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify it is valid JSON
	var parsed []registry.ClaimEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, output)
	}

	// Verify entries
	if len(parsed) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(parsed))
	}

	if parsed[0].Name != "my-vm" {
		t.Errorf("expected first entry name my-vm, got %s", parsed[0].Name)
	}
	if parsed[0].Template != "vsphere-vm" {
		t.Errorf("expected first entry template vsphere-vm, got %s", parsed[0].Template)
	}
	if parsed[0].Category != "infra" {
		t.Errorf("expected first entry category infra, got %s", parsed[0].Category)
	}

	if parsed[1].Name != "my-db" {
		t.Errorf("expected second entry name my-db, got %s", parsed[1].Name)
	}
	if parsed[1].Template != "postgres" {
		t.Errorf("expected second entry template postgres, got %s", parsed[1].Template)
	}
	if parsed[1].Namespace != "production" {
		t.Errorf("expected second entry namespace production, got %s", parsed[1].Namespace)
	}
}
