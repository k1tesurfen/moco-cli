package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
	"github.com/k1tesurfen/moco-cli/internal/wizard"
)

// activityForm is the in-TUI popup for adding or editing an activity and for starting a timer:
// one fuzzy list of project / task pairs (recent and aliases first), then duration and
// description. It never leaves the TUI.
type activityForm struct {
	title    string
	timer    bool // timer start: pair + optional description, no duration
	joinOnly bool // only the projects the user can join, no task / duration / description

	// Projects the user is not on are listed (tag "+") once a filter is typed; enter on one
	// calls onJoin, and joined (from the model) turns it into the project's tasks.
	onJoin      func(p api.Project) tea.Cmd
	joinPending bool   // the joinable projects are still loading
	joining     string // name of the project being joined

	options []pairOption
	matches []int // indexes into options for the current filter
	sel     int   // index into matches
	offset  int
	chosen  *service.Pick

	filter textinput.Model
	dur    textinput.Model
	desc   textinput.Model
	focus  int // 0 pair, 1 duration, 2 description

	fallback     int // seconds used for an empty duration (0 = required)
	fallbackHint string
	round        func(int) int
	stepMinutes  int

	err    string
	submit func(f *activityForm, seconds int) (tea.Cmd, error)
}

type pairOption struct {
	tag    string // "↺" recent, "@alias", "+" join
	pick   service.Pick
	search string
	join   bool // a project the user is not on; pick has no task
}

const (
	fPair = iota
	fDuration
	fDescription
)

const formListRows = 8

// cursorMode is the text cursor of all inputs; tests use a static one (no blink timers).
var cursorMode = cursor.CursorBlink

func newInput(value, placeholder string) textinput.Model {
	in := textinput.New()
	in.Cursor.SetMode(cursorMode)
	in.Prompt = "› "
	in.PromptStyle = sKey
	in.Cursor.Style = sKey
	in.PlaceholderStyle = sMuted
	in.Placeholder = placeholder
	in.CharLimit = 2000
	in.SetValue(value)
	in.CursorEnd()
	return in
}

// newActivityForm builds the pair list from the wizard environment (recents, aliases, projects).
func newActivityForm(title string, env wizard.Env) *activityForm {
	f := &activityForm{
		title:       title,
		filter:      newInput("", "type to filter: project, customer, task or @alias"),
		dur:         newInput("", "e.g. 1h30, 90m, 1:30, 1.5"),
		desc:        newInput("", "what did you do?"),
		round:       env.Round,
		stepMinutes: env.RoundingMinutes,
	}
	seen := map[[2]int64]bool{}
	add := func(tag string, p service.Pick) {
		key := [2]int64{p.Project.ID, p.Task.ID}
		if seen[key] {
			return
		}
		seen[key] = true
		search := strings.ToLower(strings.Join([]string{tag, p.Project.Name, p.Project.Customer.Name, p.Project.Identifier, p.Task.Name}, " "))
		f.options = append(f.options, pairOption{tag: tag, pick: p, search: search})
	}
	for _, r := range env.Recent {
		add("↺", r)
	}
	names := make([]string, 0, len(env.Aliases))
	for n := range env.Aliases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		add("@"+n, env.Aliases[n])
	}
	for _, p := range env.Projects {
		for _, t := range service.ActiveTasks(p) {
			add("", service.Pick{Project: p, Task: t})
		}
	}
	f.applyFilter()
	f.setFocus(fPair)
	return f
}

// newJoinForm is the popup for joining a project: only the joinable projects, enter joins.
func newJoinForm() *activityForm {
	f := &activityForm{
		title:       "Join a project",
		joinOnly:    true,
		joinPending: true,
		filter:      newInput("", "type to search all company projects: name, customer or identifier"),
	}
	f.applyFilter()
	f.setFocus(fPair)
	return f
}

func projectSearch(p api.Project) string {
	return strings.ToLower(strings.Join([]string{p.Name, p.Customer.Name, p.Identifier}, " "))
}

// setJoinable replaces the join options with ps (the projects the user is not on).
func (f *activityForm) setJoinable(ps []api.Project) {
	cur := f.selected()
	f.joinPending = false
	opts := f.options[:0:0]
	for _, o := range f.options {
		if !o.join {
			opts = append(opts, o)
		}
	}
	for _, p := range ps {
		opts = append(opts, pairOption{tag: "+", pick: service.Pick{Project: p}, search: projectSearch(p), join: true})
	}
	f.options = opts
	f.applyFilter()
	f.reselect(cur)
}

// joined replaces the join option of p by its tasks and selects the first of them.
func (f *activityForm) joined(p api.Project) {
	f.joining = ""
	var opts, tasks []pairOption
	for _, o := range f.options {
		if !(o.join && o.pick.Project.ID == p.ID) {
			opts = append(opts, o)
		}
	}
	for _, t := range service.ActiveTasks(p) {
		pick := service.Pick{Project: p, Task: t}
		tasks = append(tasks, pairOption{pick: pick, search: projectSearch(p) + " " + strings.ToLower(t.Name)})
	}
	i := len(opts)
	for j, o := range opts {
		if o.join {
			i = j
			break
		}
	}
	f.options = append(opts[:i:i], append(tasks, opts[i:]...)...)
	f.applyFilter()
	for j, idx := range f.matches {
		if f.options[idx].pick.Project.ID == p.ID {
			f.sel = j
			break
		}
	}
	if len(tasks) == 0 {
		f.err = p.Name + " has no active tasks to book on."
	}
}

// selected is the highlighted option, nil if nothing matches.
func (f *activityForm) selected() *pairOption {
	if f.sel < len(f.matches) {
		return &f.options[f.matches[f.sel]]
	}
	return nil
}

// reselect moves the cursor back onto o (after the option list changed).
func (f *activityForm) reselect(o *pairOption) {
	if o == nil {
		return
	}
	for j, idx := range f.matches {
		if q := f.options[idx]; q.join == o.join && q.pick.Project.ID == o.pick.Project.ID && q.pick.Task.ID == o.pick.Task.ID {
			f.sel = j
		}
	}
}

// preselect sets the current pair (edit mode) and moves the list cursor onto it.
func (f *activityForm) preselect(p service.Pick) {
	f.chosen = &p
	for i, idx := range f.matches {
		if o := f.options[idx].pick; o.Project.ID == p.Project.ID && o.Task.ID == p.Task.ID {
			f.sel = i
		}
	}
}

func (f *activityForm) applyFilter() {
	words := strings.Fields(strings.ToLower(f.filter.Value()))
	f.matches = f.matches[:0]
	for i, o := range f.options {
		if o.join && len(words) == 0 && !f.joinOnly {
			continue // projects to join only show up when searching
		}
		if containsAll(o.search, words) {
			f.matches = append(f.matches, i)
		}
	}
	f.sel, f.offset = 0, 0
}

func containsAll(hay string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

func (f *activityForm) fields() []int {
	if f.joinOnly {
		return []int{fPair}
	}
	if f.timer {
		return []int{fPair, fDescription}
	}
	return []int{fPair, fDuration, fDescription}
}

func (f *activityForm) setFocus(field int) {
	f.focus = field
	f.filter.Blur()
	f.dur.Blur()
	f.desc.Blur()
	switch field {
	case fPair:
		f.filter.Focus()
	case fDuration:
		f.dur.Focus()
	case fDescription:
		f.desc.Focus()
	}
}

func (f *activityForm) move(d int) {
	fs := f.fields()
	i := 0
	for j, x := range fs {
		if x == f.focus {
			i = j
		}
	}
	f.setFocus(fs[(i+d+len(fs))%len(fs)])
}

func (f *activityForm) setWidth(w int) {
	for _, in := range []*textinput.Model{&f.filter, &f.dur, &f.desc} {
		in.Width = max(10, w-8)
	}
}

// seconds parses the duration field; empty means the fallback.
func (f *activityForm) seconds() (int, error) {
	s := strings.TrimSpace(f.dur.Value())
	if s == "" {
		if f.fallback > 0 {
			return f.fallback, nil
		}
		return 0, errors.New("a duration is required")
	}
	return timeutil.ParseDuration(s)
}

// update handles a key; done reports whether the form closes.
func (f *activityForm) update(msg tea.KeyMsg) (cmd tea.Cmd, done bool) {
	switch msg.String() {
	case "esc":
		return nil, true
	case "tab":
		f.pickSelected()
		f.move(1)
		return nil, false
	case "shift+tab":
		f.pickSelected()
		f.move(-1)
		return nil, false
	}

	if f.focus == fPair {
		switch msg.String() {
		case "up", "ctrl+p", "ctrl+k":
			f.sel = max(0, f.sel-1)
			return nil, false
		case "down", "ctrl+n", "ctrl+j":
			f.sel = clamp(f.sel+1, 0, len(f.matches)-1)
			return nil, false
		case "enter":
			if o := f.selected(); o != nil && o.join {
				if f.joining == "" && f.onJoin != nil {
					f.joining, f.err = o.pick.Project.Name, ""
					return f.onJoin(o.pick.Project), false
				}
				return nil, false
			}
			if !f.pickSelected() {
				f.err = "no project / task matches the filter"
				return nil, false
			}
			f.move(1)
			return nil, false
		}
		before := f.filter.Value()
		var c tea.Cmd
		f.filter, c = f.filter.Update(msg)
		if f.filter.Value() != before {
			f.err = ""
			f.applyFilter()
		}
		return c, false
	}

	if msg.String() == "enter" {
		if f.focus != fDescription {
			f.move(1)
			return nil, false
		}
		return f.trySubmit()
	}
	f.err = ""
	var c tea.Cmd
	if f.focus == fDuration {
		f.dur, c = f.dur.Update(msg)
	} else {
		f.desc, c = f.desc.Update(msg)
	}
	return c, false
}

// pickSelected takes the highlighted pair as the choice.
func (f *activityForm) pickSelected() bool {
	if o := f.selected(); f.focus != fPair || o == nil || o.join {
		return f.chosen != nil
	}
	p := f.options[f.matches[f.sel]].pick
	f.chosen = &p
	return true
}

func (f *activityForm) trySubmit() (tea.Cmd, bool) {
	var sec int
	var err error
	switch {
	case f.chosen == nil:
		err = errors.New("choose a project / task first")
		f.setFocus(fPair)
	case !f.timer:
		if sec, err = f.seconds(); err != nil {
			f.setFocus(fDuration)
		} else if strings.TrimSpace(f.desc.Value()) == "" {
			err = errors.New("a description is required")
		}
	}
	if err == nil {
		var cmd tea.Cmd
		if cmd, err = f.submit(f, sec); err == nil {
			return cmd, true
		}
	}
	f.err = err.Error()
	return nil, false
}

// lines renders the form for a popup of content width w.
func (f *activityForm) lines(w int) []string {
	var out []string
	label := func(field int, text, hint string) string {
		l := sMuted.Render(text)
		if f.focus == field {
			l = sSection.Render(text)
		}
		if hint != "" {
			l += "  " + sMuted.Render(hint)
		}
		return l
	}

	if f.joinOnly {
		out = append(out, label(fPair, "Project", "projects you are not on yet · enter adds you to the team"))
	} else {
		chosen := sMuted.Render("none yet")
		if f.chosen != nil {
			chosen = sProject.Render(f.chosen.Project.Name + " / " + f.chosen.Task.Name)
		}
		out = append(out, label(fPair, "Project / task", "")+"  "+chosen)
	}
	if f.focus == fPair {
		out = append(out, f.filter.View())
		searching := f.joinPending && (f.joinOnly || strings.TrimSpace(f.filter.Value()) != "")
		switch {
		case len(f.matches) == 0 && searching:
			out = append(out, sMuted.Render("  searching all company projects…"))
		case len(f.matches) == 0:
			out = append(out, sMuted.Render("  no match"))
		}
		if f.sel < f.offset {
			f.offset = f.sel
		}
		if f.sel >= f.offset+formListRows {
			f.offset = f.sel - formListRows + 1
		}
		for i := f.offset; i < len(f.matches) && i < f.offset+formListRows; i++ {
			o := f.options[f.matches[i]]
			tag := ""
			if o.tag != "" {
				tag = sKey.Render(o.tag) + " "
			}
			var row string
			if o.join {
				row = " " + tag + sProject.Render(clip(o.pick.Project.Name, w-4)) +
					sMuted.Render(" · "+o.pick.Project.Customer.Name+" · join")
			} else {
				row = " " + tag + sProject.Render(pairLabel(o.pick.Project.Name, o.pick.Task.Name, w-4-len([]rune(o.tag)))) +
					sMuted.Render(" · "+o.pick.Project.Customer.Name)
			}
			if i == f.sel {
				row = highlight(row, w, true)
			}
			out = append(out, row)
		}
		if n := len(f.matches); n > formListRows {
			verb := "take"
			if f.joinOnly {
				verb = "join"
			}
			out = append(out, sMuted.Render(fmt.Sprintf("  %d of %d · ↑↓ select · enter %s", f.sel+1, n, verb)))
		}
		switch o := f.selected(); {
		case f.joining != "":
			out = append(out, sWarn.Render("  Joining "+f.joining+"…"))
		case o != nil && o.join:
			out = append(out, sMuted.Render("  + you are not on this project · enter adds you to its team in MOCO"))
		}
	}
	if f.joinOnly {
		if f.err != "" {
			out = append(out, "", sErr.Render(f.err))
		}
		return out
	}

	if !f.timer {
		out = append(out, "", label(fDuration, "Duration", f.fallbackHint), f.dur.View())
		if sec, err := f.seconds(); err == nil {
			out = append(out, sMuted.Render("  → ")+f.preview(sec))
		}
	}
	descHint := ""
	if f.timer {
		descHint = "optional — asked when the timer stops"
	}
	out = append(out, "", label(fDescription, "Description", descHint), f.desc.View())
	if f.err != "" {
		out = append(out, "", sErr.Render(f.err))
	}
	return out
}

// preview renders "1h07 → 1h15 (rounded up to 15 min)" or just "1h15".
func (f *activityForm) preview(sec int) string {
	r := sec
	if f.round != nil {
		r = f.round(sec)
	}
	if r == sec {
		return timeutil.FormatSeconds(sec)
	}
	return fmt.Sprintf("%s → %s %s", timeutil.FormatSeconds(sec), sOK.Render(timeutil.FormatSeconds(r)),
		sMuted.Render(fmt.Sprintf("(rounded up to %d min)", f.stepMinutes)))
}
