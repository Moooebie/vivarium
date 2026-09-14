package tui

import "github.com/charmbracelet/lipgloss"

// Theme bundles the lipgloss styles used across the TUI.
type Theme struct {
	Title      lipgloss.Style
	Subtitle   lipgloss.Style
	Dim        lipgloss.Style
	Selected   lipgloss.Style
	Header     lipgloss.Style
	Button     lipgloss.Style
	ButtonHot  lipgloss.Style
	Error      lipgloss.Style
	Success    lipgloss.Style
	FieldLabel lipgloss.Style
	FieldFocus lipgloss.Style
	Badge      lipgloss.Style
	Border     lipgloss.Style
	Modal      lipgloss.Style
}

var (
	colorPrimary = lipgloss.Color("205")
	colorDim     = lipgloss.Color("240")
	colorGreen   = lipgloss.Color("42")
	colorYellow  = lipgloss.Color("214")
	colorRed     = lipgloss.Color("203")
	colorCyan    = lipgloss.Color("51")
)

// DefaultTheme returns the standard Vivarium theme.
func DefaultTheme() Theme {
	return Theme{
		Title:      lipgloss.NewStyle().Bold(true).Foreground(colorPrimary),
		Subtitle:   lipgloss.NewStyle().Foreground(colorDim),
		Dim:        lipgloss.NewStyle().Foreground(colorDim),
		Selected:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(colorPrimary),
		Header:     lipgloss.NewStyle().Bold(true).Foreground(colorCyan),
		Button:     lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("252")),
		ButtonHot:  lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(lipgloss.Color("230")).Background(colorPrimary),
		Error:      lipgloss.NewStyle().Foreground(colorRed),
		Success:    lipgloss.NewStyle().Foreground(colorGreen),
		FieldLabel: lipgloss.NewStyle().Foreground(colorDim),
		FieldFocus: lipgloss.NewStyle().Bold(true).Foreground(colorPrimary),
		Badge:      lipgloss.NewStyle().Padding(0, 1),
		Border:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1),
		Modal:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).BorderForeground(colorPrimary),
	}
}

// StatusBadge renders a colored status badge.
func (t Theme) StatusBadge(status string) string {
	switch status {
	case "running":
		return t.Badge.Foreground(colorGreen).Render("RUNNING")
	case "halted":
		return t.Badge.Foreground(colorDim).Render("HALTED")
	case "building":
		return t.Badge.Foreground(colorYellow).Render("BUILDING")
	case "error":
		return t.Badge.Foreground(colorRed).Render("ERROR")
	default:
		return t.Badge.Render(status)
	}
}
