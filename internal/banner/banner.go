package banner

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/mattn/go-isatty"
)

const bannerText = ` ██████╗██╗      █████╗ ██╗███╗   ███╗███████╗
██╔════╝██║     ██╔══██╗██║████╗ ████║██╔════╝
██║     ██║     ███████║██║██╔████╔██║███████╗
██║     ██║     ██╔══██║██║██║╚██╔╝██║╚════██║
╚██████╗███████╗██║  ██║██║██║ ╚═╝ ██║███████║
 ╚═════╝╚══════╝╚═╝  ╚═╝╚═╝╚═╝     ╚═╝╚══════╝`

const glitchChars = "░▒▓█▄▀▐▌╠╣╬═║╗╝╚╔"

var (
	// Gradient endpoints for the static logo (magenta -> cyan)
	gradientStart, _ = colorful.Hex("#ff00ff")
	gradientEnd, _   = colorful.Hex("#00ffff")

	glitchStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e879f9")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4a044e"))

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888")).
			Italic(true)

	dimInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))
)

type tickMsg time.Time

type model struct {
	width        int
	frame        int
	glitchPhase  bool
	glitchFrames int
	done         bool
}

// Version info, set from cmd package via SetVersionInfo before calling Show.
var (
	versionStr = "dev"
	commitStr  = "none"
	buildStr   = "unknown"
)

// SetVersionInfo sets version details displayed in the banner.
func SetVersionInfo(version, commit, buildDate string) {
	versionStr = version
	commitStr = commit
	buildStr = buildDate
}

// Show displays the animated banner when running in a TTY,
// or prints a static logo otherwise.
func Show() {
	if !isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		fmt.Println(renderGradient())
		return
	}

	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	_, _ = p.Run()
	fmt.Println(renderGradient())
}

// renderGradient renders the banner with the magenta-to-cyan per-character gradient.
func renderGradient() string {
	lines := strings.Split(bannerText, "\n")

	maxWidth := 0
	for _, line := range lines {
		if len([]rune(line)) > maxWidth {
			maxWidth = len([]rune(line))
		}
	}

	var result strings.Builder
	result.WriteString("\n")
	for _, line := range lines {
		runes := []rune(line)
		for i, ch := range runes {
			if ch == ' ' {
				result.WriteRune(ch)
				continue
			}
			t := float64(i) / float64(maxWidth)
			c := gradientStart.BlendLuv(gradientEnd, t)
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex()))
			result.WriteString(style.Render(string(ch)))
		}
		result.WriteString("\n")
	}

	// Center the subtitle relative to the banner width
	subtitle := "rendering your resource claims since '26"
	pad := (maxWidth - len(subtitle)) / 2
	if pad < 0 {
		pad = 0
	}
	result.WriteString(subtitleStyle.Render(strings.Repeat(" ", pad) + subtitle))
	result.WriteString("\n\n")

	// Version info
	versionLine := fmt.Sprintf("Version: %s | Commit: %s | Built: %s", versionStr, commitStr, buildStr)
	vPad := (maxWidth - len(versionLine)) / 2
	if vPad < 0 {
		vPad = 0
	}
	result.WriteString(dimInfoStyle.Render(strings.Repeat(" ", vPad) + versionLine))
	result.WriteString("\n\n")
	return result.String()
}

func initialModel() model {
	return model{
		width:        80,
		glitchPhase:  true,
		glitchFrames: 0,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Init() tea.Cmd {
	return tickCmd()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.done = true
		return m, tea.Quit

	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tickMsg:
		_ = msg
		m.frame++

		if m.glitchPhase {
			m.glitchFrames++
			if m.glitchFrames >= 10 {
				m.glitchPhase = false
			}
			return m, tickCmd()
		}

		// After glitch, hold the gradient logo briefly then exit
		if m.frame >= 20 {
			m.done = true
			return m, tea.Quit
		}

		return m, tickCmd()
	}

	return m, nil
}

func (m model) View() string {
	if m.done {
		return ""
	}

	var output string
	if m.glitchPhase {
		output = glitchText(bannerText, m.glitchFrames)
	} else {
		// Post-glitch: show the gradient logo (same as final static output)
		output = renderGradient()
	}

	return applyScanlines(centerText(output, m.width))
}

func glitchText(text string, glitchFrame int) string {
	glitchProbability := float64(10-glitchFrame) / 10.0
	if glitchProbability < 0 {
		glitchProbability = 0
	}

	runes := []rune(text)
	glitchRunes := []rune(glitchChars)
	result := make([]rune, len(runes))

	for i, r := range runes {
		if r == '\n' || r == ' ' {
			result[i] = r
			continue
		}
		if rand.Float64() < glitchProbability {
			result[i] = glitchRunes[rand.IntN(len(glitchRunes))]
		} else {
			result[i] = r
		}
	}

	return glitchStyle.Render(string(result))
}

func applyScanlines(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i%2 == 1 {
			lines[i] = dimStyle.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

func centerText(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		visLen := lipgloss.Width(line)
		if visLen < width {
			pad := (width - visLen) / 2
			lines[i] = strings.Repeat(" ", pad) + line
		}
	}
	return strings.Join(lines, "\n")
}
