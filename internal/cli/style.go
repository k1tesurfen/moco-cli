package cli

import (
	"bytes"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The plain command output uses the same 16 ANSI colours as the wizard and the TUI. lipgloss
// drops the colours when stdout is not a terminal, so piped output stays plain.
var (
	outTitle   = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	outHead    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).TabWidth(lipgloss.NoTabConversion)
	outMuted   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	outProject = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	outOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	outWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	outErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// table is a drop-in for text/tabwriter (cells separated by \t, rows by \n) that measures cells
// without ANSI escape codes, so coloured cells stay aligned. The last cell of a row is not padded.
type table struct {
	out io.Writer
	buf bytes.Buffer
}

func newTable(out io.Writer) *table { return &table{out: out} }

func (t *table) Write(p []byte) (int, error) { return t.buf.Write(p) }

// Flush writes the aligned rows.
func (t *table) Flush() error {
	text := strings.TrimSuffix(t.buf.String(), "\n")
	t.buf.Reset()
	if text == "" {
		return nil
	}
	var rows [][]string
	var widths []int
	for _, line := range strings.Split(text, "\n") {
		cells := strings.Split(line, "\t")
		for i, c := range cells[:len(cells)-1] {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], lipgloss.Width(c))
		}
		rows = append(rows, cells)
	}
	var b strings.Builder
	for _, cells := range rows {
		for i, c := range cells {
			b.WriteString(c)
			if i < len(cells)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(c)+2))
			}
		}
		b.WriteString("\n")
	}
	_, err := io.WriteString(t.out, b.String())
	return err
}
