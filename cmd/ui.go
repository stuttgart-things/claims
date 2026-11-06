package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
