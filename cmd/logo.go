package cmd

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
)

const logoRaw = `


 ██████╗██╗      █████╗ ██╗███╗   ███╗███████╗
██╔════╝██║     ██╔══██╗██║████╗ ████║██╔════╝
██║     ██║     ███████║██║██╔████╔██║███████╗
██║     ██║     ██╔══██║██║██║╚██╔╝██║╚════██║
╚██████╗███████╗██║  ██║██║██║ ╚═╝ ██║███████║
 ╚═════╝╚══════╝╚═╝  ╚═╝╚═╝╚═╝     ╚═╝╚══════╝
`

var (
	gradientStart = "#ff00ff" // neon magenta
	gradientEnd   = "#00ffff" // electric cyan
)

func renderLogo() string {
	lines := strings.Split(strings.TrimPrefix(logoRaw, "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}

	// Find the max width for gradient calculation
	maxWidth := 0
	for _, line := range lines {
		if len(line) > maxWidth {
			maxWidth = len(line)
		}
	}

	startColor, _ := colorful.Hex(gradientStart)
	endColor, _ := colorful.Hex(gradientEnd)

	var result strings.Builder
	for _, line := range lines {
		for i, char := range line {
			if char == ' ' {
				result.WriteRune(char)
				continue
			}
			// Calculate gradient position based on horizontal position
			t := float64(i) / float64(maxWidth)
			c := startColor.BlendLuv(endColor, t)
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex()))
			result.WriteString(style.Render(string(char)))
		}
		result.WriteString("\n")
	}

	return result.String()
}

// Logo returns the rendered logo with gradient colors
var logo = renderLogo()
