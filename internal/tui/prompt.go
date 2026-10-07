package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// field is one input of a prompt.
type field struct {
	label string
	hint  string
	input textinput.Model
}

// prompt is a small modal form. submit validates the values and returns the command to run;
// a non-nil error is shown under the form and the prompt stays open.
type prompt struct {
	title  string
	note   string
	fields []field
	focus  int
	err    string
	submit func(values []string) (tea.Cmd, error)
}

func newPrompt(title string, submit func([]string) (tea.Cmd, error)) *prompt {
	return &prompt{title: title, submit: submit}
}

// add appends an input with an initial value.
func (p *prompt) add(label, value, hint string) *prompt {
	in := textinput.New()
	in.Prompt = "› "
	in.PromptStyle = sCursor
	in.Cursor.Style = sCursor
	in.PlaceholderStyle = sMuted
	in.CharLimit = 2000
	in.SetValue(value)
	in.CursorEnd()
	if len(p.fields) == 0 {
		in.Focus()
	}
	p.fields = append(p.fields, field{label: label, hint: hint, input: in})
	return p
}

func (p *prompt) setWidth(w int) {
	for i := range p.fields {
		p.fields[i].input.Width = max(10, w-8)
	}
}

func (p *prompt) move(d int) {
	p.fields[p.focus].input.Blur()
	p.focus = (p.focus + d + len(p.fields)) % len(p.fields)
	p.fields[p.focus].input.Focus()
}

// update handles a key; done reports whether the prompt closes.
func (p *prompt) update(msg tea.KeyMsg) (cmd tea.Cmd, done bool) {
	switch msg.String() {
	case "esc":
		return nil, true
	case "tab", "down":
		p.move(1)
		return nil, false
	case "shift+tab", "up":
		p.move(-1)
		return nil, false
	case "enter":
		if p.focus < len(p.fields)-1 {
			p.move(1)
			return nil, false
		}
		values := make([]string, len(p.fields))
		for i, f := range p.fields {
			values[i] = strings.TrimSpace(f.input.Value())
		}
		cmd, err := p.submit(values)
		if err != nil {
			p.err = err.Error()
			return nil, false
		}
		return cmd, true
	}
	p.err = ""
	var c tea.Cmd
	p.fields[p.focus].input, c = p.fields[p.focus].input.Update(msg)
	return c, false
}

func (p *prompt) view() string {
	var b strings.Builder
	b.WriteString(sTitle.Render(p.title) + "\n")
	if p.note != "" {
		b.WriteString(sMuted.Render(p.note) + "\n")
	}
	for i, f := range p.fields {
		b.WriteString("\n")
		label := sMuted.Render(f.label)
		if i == p.focus {
			label = sSection.Render(f.label)
		}
		b.WriteString(label)
		if f.hint != "" {
			b.WriteString("  " + sMuted.Render(f.hint))
		}
		b.WriteString("\n" + f.input.View() + "\n")
	}
	if p.err != "" {
		b.WriteString("\n" + sErr.Render(p.err) + "\n")
	}
	b.WriteString("\n" + keys("enter", "save", "tab", "next field", "esc", "cancel"))
	return b.String()
}

// confirmBox asks a yes/no question; yes is called (on the event loop) when confirmed.
type confirmBox struct {
	question string
	detail   string
	yes      func() tea.Cmd
}

func (c *confirmBox) update(msg tea.KeyMsg) (cmd tea.Cmd, done bool) {
	switch msg.String() {
	case "y", "Y":
		return c.yes(), true
	case "n", "N", "esc", "q":
		return nil, true
	}
	return nil, false
}

func (c *confirmBox) view() string {
	s := sTitle.Render(c.question) + "\n"
	if c.detail != "" {
		s += "\n" + c.detail + "\n"
	}
	return s + "\n" + keys("y", "yes", "n", "no")
}
