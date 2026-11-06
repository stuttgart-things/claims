package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestValidateStdout(t *testing.T) {
	tests := []struct {
		name    string
		config  RenderConfig
		forceI  bool
		wantErr string
	}{
		{"plain", RenderConfig{}, false, ""},
		{"interactive", RenderConfig{}, true, "--interactive"},
		{"git", RenderConfig{GitConfig: &GitConfig{}}, false, "--git-*"},
		{"layout", RenderConfig{Layout: LayoutSourcePath}, false, "--layout"},
		{"dry-run", RenderConfig{DryRun: true}, false, "--dry-run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStdout(&tt.config, tt.forceI)
			if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestRenderStdout renders through a fake API with --stdout: stdout carries
// only the YAML, progress goes to stderr, nothing is written.
func TestRenderStdout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/order") {
			_, _ = w.Write([]byte(`{"rendered":"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n"}`))
			return
		}
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ClaimTemplateList","items":[{"metadata":{"name":"cm"},"spec":{"parameters":[{"name":"name"}]}}]}`))
	}))
	t.Cleanup(srv.Close)

	outDir := t.TempDir()
	config := &RenderConfig{
		APIUrl:          srv.URL,
		Templates:       []string{"cm"},
		InlineParamsRaw: []string{"name=demo"},
		OutputDir:       outDir,
		FilenamePattern: "{{.template}}-{{.name}}.yaml",
		Stdout:          true,
	}

	stdout, stderr := captureStd(t, func() {
		if err := runNonInteractive(config); err != nil {
			t.Fatal(err)
		}
	})

	if stdout != "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n" {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "Rendering cm") {
		t.Errorf("progress should go to stderr, got %q", stderr)
	}
	if entries, _ := os.ReadDir(outDir); len(entries) != 0 {
		t.Errorf("--stdout must not write files, found %d", len(entries))
	}
}

// captureStd runs f with os.Stdout and os.Stderr redirected to pipes.
func captureStd(t *testing.T, f func()) (string, string) {
	t.Helper()
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	outC, errC := make(chan string), make(chan string)
	go func() { var b bytes.Buffer; _, _ = io.Copy(&b, rOut); outC <- b.String() }()
	go func() { var b bytes.Buffer; _, _ = io.Copy(&b, rErr); errC <- b.String() }()
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	f()
	_ = wOut.Close()
	_ = wErr.Close()
	return <-outC, <-errC
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
