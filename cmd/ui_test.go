package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

func TestIsAbort(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"huh abort", huh.ErrUserAborted, true},
		{"wrapped huh abort", fmt.Errorf("output configuration: %w", huh.ErrUserAborted), true},
		{"abort with note", &abortError{note: "saved"}, true},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAbort(tt.err); got != tt.want {
				t.Errorf("isAbort(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestAbortMessage(t *testing.T) {
	if got := abortMessage(huh.ErrUserAborted); got != "Aborted, nothing written." {
		t.Errorf("got %q", got)
	}
	got := abortMessage(&abortError{note: "Rendered YAML saved to /tmp/x.yaml"})
	if got != "Aborted, nothing written.\nRendered YAML saved to /tmp/x.yaml" {
		t.Errorf("got %q", got)
	}
	if strings.Contains(got, "user aborted") || strings.Contains(got, "Error") {
		t.Errorf("abort message must not look like an error: %q", got)
	}
}

func TestStyleLinesNoTrailingSpaces(t *testing.T) {
	style := lipgloss.NewStyle().Bold(true)
	out := styleLines(style, "short\n\na much longer line")
	for i, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(l, " ") {
			t.Errorf("line %d has trailing spaces: %q", i, l)
		}
	}
}

func TestRenderedYAML(t *testing.T) {
	got := renderedYAML([]RenderResult{
		{Content: "kind: A\n"},
		{Error: errors.New("failed")},
		{Content: "---\nkind: B\n---\nkind: C"},
	})
	want := "---\nkind: A\n---\nkind: B\n---\nkind: C\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestExitOnAbort runs exitOn in a subprocess: an abort exits 130 with the
// short note on stderr, no error prefix.
func TestExitOnAbort(t *testing.T) {
	if os.Getenv("CLAIMS_TEST_EXIT_ON_ABORT") == "1" {
		exitOn(fmt.Errorf("output configuration: %w", huh.ErrUserAborted))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExitOnAbort$")
	cmd.Env = append(os.Environ(), "CLAIMS_TEST_EXIT_ON_ABORT=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != exitCodeAborted {
		t.Fatalf("want exit code %d, got %v", exitCodeAborted, err)
	}
	if got := strings.TrimSpace(stderr.String()); got != "Aborted, nothing written." {
		t.Errorf("stderr = %q", got)
	}
}
