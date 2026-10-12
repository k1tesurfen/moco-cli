package wizard

import (
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// The theme only uses the 16 ANSI colours, so it follows the user's terminal colour scheme
// (light or dark) instead of fixed hex values.
var (
	ansiRed     = lipgloss.Color("1")
	ansiGreen   = lipgloss.Color("2")
	ansiBlue    = lipgloss.Color("4")
	ansiMagenta = lipgloss.Color("5")
	ansiCyan    = lipgloss.Color("6")
	ansiGrey    = lipgloss.Color("8") // "bright black"
)

func theme() *huh.Theme {
	t := huh.ThemeBase()

	f := &t.Focused
	f.Base = f.Base.BorderForeground(ansiBlue)
	f.Card = f.Base
	f.Title = lipgloss.NewStyle().Foreground(ansiBlue).Bold(true)
	f.NoteTitle = f.Title.MarginBottom(1)
	f.Description = lipgloss.NewStyle().Foreground(ansiGrey)
	f.ErrorIndicator = lipgloss.NewStyle().Foreground(ansiRed).SetString(" *")
	f.ErrorMessage = lipgloss.NewStyle().Foreground(ansiRed)
	f.SelectSelector = lipgloss.NewStyle().Foreground(ansiMagenta).SetString("❯ ")
	f.Option = lipgloss.NewStyle()
	f.SelectedOption = lipgloss.NewStyle().Foreground(ansiGreen)
	f.NextIndicator = lipgloss.NewStyle().Foreground(ansiGrey).MarginLeft(1).SetString("→")
	f.PrevIndicator = lipgloss.NewStyle().Foreground(ansiGrey).MarginRight(1).SetString("←")
	f.TextInput.Cursor = lipgloss.NewStyle().Foreground(ansiMagenta)
	f.TextInput.Placeholder = lipgloss.NewStyle().Foreground(ansiGrey)
	f.TextInput.Prompt = lipgloss.NewStyle().Foreground(ansiMagenta)
	f.TextInput.Text = lipgloss.NewStyle()
	button := lipgloss.NewStyle().Padding(0, 2).MarginRight(1)
	f.FocusedButton = button.Foreground(lipgloss.Color("0")).Background(ansiGreen).Bold(true)
	f.BlurredButton = button.Foreground(ansiGrey)

	t.Blurred = *f
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = lipgloss.NewStyle().Foreground(ansiGrey)
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Help.ShortKey = lipgloss.NewStyle().Foreground(ansiGrey)
	t.Help.ShortDesc = lipgloss.NewStyle().Foreground(ansiGrey).Faint(true)
	t.Help.ShortSeparator = t.Help.ShortDesc
	return t
}

// Styles for the confirmation summary.
var (
	styleAccent = lipgloss.NewStyle().Foreground(ansiCyan).Bold(true)
	styleMuted  = lipgloss.NewStyle().Foreground(ansiGrey)
)
