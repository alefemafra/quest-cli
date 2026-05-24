package internal

import "github.com/charmbracelet/lipgloss"

type Styles struct {
	Title       lipgloss.Style
	Separator   lipgloss.Style
	UserLabel   lipgloss.Style
	ClaudeLabel lipgloss.Style
	SystemText  lipgloss.Style
	Prompt      lipgloss.Style
	Dim         lipgloss.Style
	Hint        lipgloss.Style
	Cyan        lipgloss.Style
	Green       lipgloss.Style
	Red         lipgloss.Style
	StatusDone  lipgloss.Style
	StatusWIP   lipgloss.Style
	StatusBlock lipgloss.Style
	StatusPend       lipgloss.Style
	StatusValidating lipgloss.Style
	StatusRefining   lipgloss.Style
	Yellow           lipgloss.Style
	Magenta          lipgloss.Style
	Blue             lipgloss.Style
	TabActive   lipgloss.Style
	TabInactive lipgloss.Style
}

func NewStyles() Styles {
	return Styles{
		Title:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("226")),
		Separator:   lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		UserLabel:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33")),
		ClaudeLabel: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("226")),
		SystemText:  lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Prompt:      lipgloss.NewStyle().Foreground(lipgloss.Color("226")),
		Dim:         lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Hint:        lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Cyan:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("36")),
		Green:       lipgloss.NewStyle().Foreground(lipgloss.Color("34")),
		Red:         lipgloss.NewStyle().Foreground(lipgloss.Color("160")),
		StatusDone:  lipgloss.NewStyle().Foreground(lipgloss.Color("34")),
		StatusWIP:   lipgloss.NewStyle().Foreground(lipgloss.Color("226")),
		StatusBlock: lipgloss.NewStyle().Foreground(lipgloss.Color("160")),
		StatusPend:       lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		StatusValidating: lipgloss.NewStyle().Foreground(lipgloss.Color("183")),
		StatusRefining:   lipgloss.NewStyle().Foreground(lipgloss.Color("208")),
		Yellow:           lipgloss.NewStyle().Foreground(lipgloss.Color("226")),
		Magenta:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("183")),
		Blue:             lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75")),
		TabActive:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("226")).Underline(true),
		TabInactive: lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
	}
}
