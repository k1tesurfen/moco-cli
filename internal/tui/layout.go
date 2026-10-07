package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// box draws a lazygit-style panel (content width = width − 4): rounded border with the title in the top edge and an optional
// footer in the bottom edge. cursor (-1 = none) is kept visible by scrolling; *offset remembers
// the scroll position between frames.
type box struct {
	title   string
	footer  string
	lines   []string
	right   []string // optional right-aligned column, parallel to lines
	cursor  int
	focused bool
	offset  *int
}

func (b box) render(w, h int) string {
	if w < 4 || h < 2 {
		return ""
	}
	iw, ih := w-2, h-2
	border := sBorder
	title := sMuted.Render(b.title)
	if b.focused {
		border = sBorderOn
		title = sTitleOn.Render(b.title)
	}

	off := 0
	if b.offset != nil {
		off = *b.offset
	}
	if b.cursor >= 0 && ih > 0 {
		if b.cursor < off {
			off = b.cursor
		}
		if b.cursor >= off+ih {
			off = b.cursor - ih + 1
		}
	}
	off = clamp(off, 0, len(b.lines)-ih)
	if b.offset != nil {
		*b.offset = off
	}
	footer := b.footer
	if len(b.lines) > ih && b.cursor >= 0 {
		footer = strings.TrimSpace(footer + "  " + itoa(b.cursor+1) + " of " + itoa(len(b.lines)))
	}

	var out strings.Builder
	t := clip(" "+title+" ", iw-1)
	out.WriteString(border.Render("╭─") + t + border.Render(strings.Repeat("─", max(0, iw-1-lipgloss.Width(t)))+"╮") + "\n")
	cw := iw - 2 // one space of padding on each side
	for i := 0; i < ih; i++ {
		line := ""
		if j := off + i; j < len(b.lines) {
			line = b.lines[j]
			if j < len(b.right) && b.right[j] != "" {
				rw := lipgloss.Width(b.right[j])
				line = pad(clip(line, cw-rw-1), cw-rw) + b.right[j]
			}
			line = " " + pad(clip(line, cw), cw) + " "
			if j == b.cursor {
				line = highlight(line, iw, b.focused)
			}
		}
		out.WriteString(border.Render("│") + pad(line, iw) + border.Render("│") + "\n")
	}
	f := ""
	if footer != "" {
		f = clip(" "+footer+" ", iw-1)
	}
	out.WriteString(border.Render("╰"+strings.Repeat("─", max(0, iw-1-lipgloss.Width(f)))) + f + border.Render("─╯"))
	return out.String()
}

// highlight renders the selected row as a full-width bar (focused panel) or in bold.
func highlight(line string, w int, focused bool) string {
	plain := pad(clip(ansi.Strip(line), w), w)
	if focused {
		return sSelOn.Render(plain)
	}
	return sSelOff.Render(plain)
}

// overlay draws fg centered on top of bg (both multi-line, ANSI-aware).
func overlay(bg, fg string, width int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")
	fw := lipgloss.Width(fg)
	top := max(0, (len(bgLines)-len(fgLines))/2)
	left := max(0, (width-fw)/2)
	for i, l := range fgLines {
		j := top + i
		if j >= len(bgLines) {
			break
		}
		row := bgLines[j]
		bgLines[j] = pad(ansi.Truncate(row, left, ""), left) + l + ansi.TruncateLeft(row, left+lipgloss.Width(l), "")
	}
	return strings.Join(bgLines, "\n")
}

func itoa(n int) string { return strconv.Itoa(n) }
