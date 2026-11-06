package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
)

// styleLines renders s line by line. lipgloss pads every line of a
// multi-line Render to the widest one, which leaves lines full of trailing
// spaces (and turns a leading "\n" into a line of spaces); rendering each
// line on its own keeps the output free of them.
func styleLines(style lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = style.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

// printSection prints a section header with exactly one blank line before
// it, the rhythm every "━━━ … ━━━" header of the interactive flows uses.
func printSection(style lipgloss.Style, title string) {
	fmt.Println()
	fmt.Println(style.Render(title))
}

// printError prints err in the error style to stderr.
func printError(err error) {
	fmt.Fprintln(os.Stderr, styleLines(errorStyle, err.Error()))
}

// exitCodeAborted is the conventional exit code after Ctrl+C (128+SIGINT).
const exitCodeAborted = 130

// errAborted marks an interactive flow the user aborted (Ctrl+C / Esc in a
// form). It is not a failure: exitOn prints a short note, no error prefix,
// and exits with 130.
var errAborted = errors.New("aborted")

// abortError is an abort with a note for the user (e.g. where the rendered
// output was rescued to).
type abortError struct{ note string }

func (e *abortError) Error() string { return "aborted" }
func (e *abortError) Is(target error) bool {
	return target == errAborted
}

// isAbort reports whether err is a user abort of a form.
func isAbort(err error) bool {
	return errors.Is(err, huh.ErrUserAborted) || errors.Is(err, errAborted)
}

// abortMessage is what an aborted command prints instead of an error.
func abortMessage(err error) string {
	msg := "Aborted, nothing written."
	var ae *abortError
	if errors.As(err, &ae) && ae.note != "" {
		msg += "\n" + ae.note
	}
	return msg
}

// exitOn ends the command: nothing on success, a short note and exit 130
// after a user abort, the error (stderr) and exit 1 otherwise.
func exitOn(err error) {
	if err == nil {
		return
	}
	if isAbort(err) {
		fmt.Fprintln(os.Stderr, abortMessage(err))
		os.Exit(exitCodeAborted)
	}
	printError(err)
	os.Exit(1)
}

// renderedYAML joins the successful results into one YAML stream.
func renderedYAML(results []RenderResult) string {
	var sb strings.Builder
	for _, r := range results {
		if r.Error != nil || strings.TrimSpace(r.Content) == "" {
			continue
		}
		content := strings.TrimSpace(r.Content)
		if !strings.HasPrefix(content, "---") {
			sb.WriteString("---\n")
		}
		sb.WriteString(content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// rescueRendered runs after an abort with rendered results: it offers to
// print them or save them to a temp file, and returns a note for the abort
// message ("" when nothing was kept). An abort of this prompt discards.
func rescueRendered(results []RenderResult) string {
	content := renderedYAML(results)
	if content == "" || !isTTY(os.Stdin) {
		return ""
	}

	choice := "discard"
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Keep the rendered YAML?").
			Description("Nothing has been written yet.").
			Options(
				huh.NewOption("Discard", "discard"),
				huh.NewOption("Print to stdout", "stdout"),
				huh.NewOption("Save to a temp file", "temp"),
			).
			Value(&choice),
	))
	if err := form.Run(); err != nil {
		return ""
	}

	switch choice {
	case "stdout":
		fmt.Print(content)
		return ""
	case "temp":
		path, err := writeTempYAML(content)
		if err != nil {
			return fmt.Sprintf("Could not save the rendered YAML: %v", err)
		}
		return "Rendered YAML saved to " + path
	}
	return ""
}

func writeTempYAML(content string) (string, error) {
	f, err := os.CreateTemp("", "claims-render-*.yaml")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}

func isTTY(f *os.File) bool {
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}
