package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Only the 16 ANSI colours, like the wizard: the TUI follows the terminal's colour scheme.
var (
	red     = lipgloss.Color("1")
	green   = lipgloss.Color("2")
	yellow  = lipgloss.Color("3")
	blue    = lipgloss.Color("4")
	magenta = lipgloss.Color("5")
	cyan    = lipgloss.Color("6")
	grey    = lipgloss.Color("8")

	sTitle   = lipgloss.NewStyle().Foreground(blue).Bold(true)
	sSection = lipgloss.NewStyle().Foreground(blue)
	sMuted   = lipgloss.NewStyle().Foreground(grey)
	sProject = lipgloss.NewStyle().Foreground(cyan)
	sOK      = lipgloss.NewStyle().Foreground(green)
	sWarn    = lipgloss.NewStyle().Foreground(yellow)
	sErr     = lipgloss.NewStyle().Foreground(red)
	sAlarm   = lipgloss.NewStyle().Foreground(red).Bold(true)
	sOffline = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(red).Bold(true).Padding(0, 1)
	sKey     = lipgloss.NewStyle().Foreground(magenta)

	sBorder   = lipgloss.NewStyle().Foreground(grey)
	sBorderOn = lipgloss.NewStyle().Foreground(green)
	sTitleOn  = lipgloss.NewStyle().Foreground(green).Bold(true)
	sSelOn    = lipgloss.NewStyle().Background(blue).Foreground(lipgloss.Color("15")).Bold(true)
	sSelOff   = lipgloss.NewStyle().Bold(true)
	sToday    = lipgloss.NewStyle().Foreground(blue).Bold(true)
)

// pad right-pads s (ANSI-aware) to width w.
func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// padLeft left-pads s (ANSI-aware) to width w.
func padLeft(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// clip shortens a line (ANSI-aware) to width w.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// oneLine collapses whitespace so a description fits on one row.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// keys renders "a add · e edit" style hints.
func keys(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, sKey.Render(pairs[i])+" "+sMuted.Render(pairs[i+1]))
	}
	return strings.Join(parts, sMuted.Render(" · "))
}
