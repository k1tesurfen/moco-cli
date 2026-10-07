package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// Data is loaded a week at a time (two requests) and cached, so moving between the days of a
// week costs nothing. Loads triggered by navigation are debounced: holding a key across several
// weeks only loads the week where it stops.
const (
	weekTTL       = 5 * time.Minute
	debounceDelay = 300 * time.Millisecond
)

type weekCache struct {
	days    []service.Day
	paused  map[string]bool
	fetched time.Time
	loading bool
	gen     int // bumped by invalidate; answers of older fetches are dropped
}

type weekMsg struct {
	key    string
	gen    int
	days   []service.Day
	paused map[string]bool
	err    error
}

type debounceMsg struct {
	seq  int
	kind debounceKind
}

type debounceKind int

const (
	debounceWeek debounceKind = iota
	debounceHours
)

func weekKey(t time.Time) string { return timeutil.Date(service.Monday(t)) }

// week returns the cache entry of the selected week (nil if never requested).
func (m *model) week() *weekCache { return m.weeks[weekKey(m.date)] }

// selDay returns the selected day with totals computed at the current time, or nil if its week
// is not loaded yet.
func (m *model) selDay() *service.Day {
	w := m.week()
	if w == nil || w.days == nil {
		return nil
	}
	i := int(m.date.Sub(service.Monday(m.date)).Hours()/24 + 0.5)
	if i < 0 || i >= len(w.days) {
		return nil
	}
	d := w.days[i]
	fresh, err := service.BuildDay(d.Date, d.Presences, d.Activities, m.svc.Now())
	if err != nil {
		return &d
	}
	return &fresh
}

// ensureWeek loads the selected week unless a fresh copy is cached or a load is running.
// With delay, the load waits for the navigation to settle.
func (m *model) ensureWeek(delay bool) tea.Cmd {
	w := m.week()
	if w != nil && (w.loading || (w.days != nil && m.svc.Now().Sub(w.fetched) < weekTTL)) {
		return nil
	}
	m.weekSeq++
	if delay {
		seq := m.weekSeq
		return tea.Tick(debounceDelay, func(time.Time) tea.Msg { return debounceMsg{seq, debounceWeek} })
	}
	return m.fetchWeek(weekKey(m.date))
}

func (m *model) fetchWeek(key string) tea.Cmd {
	w := m.weeks[key]
	if w == nil {
		w = &weekCache{}
		m.weeks[key] = w
	}
	w.loading = true
	gen := w.gen
	start, _ := time.ParseInLocation(timeutil.DateLayout, key, m.svc.Now().Location())
	return func() tea.Msg {
		days, err := m.svc.Days(m.ctx, start, start.AddDate(0, 0, 6))
		paused := map[string]bool{}
		if st, err := m.svc.Store.Load(); err == nil {
			for i := 0; i < 7; i++ {
				ds := timeutil.Date(start.AddDate(0, 0, i))
				paused[ds] = st.Paused(ds)
			}
		}
		return weekMsg{key, gen, days, paused, err}
	}
}

func (m *model) weekLoaded(msg weekMsg) tea.Cmd {
	w := m.weeks[msg.key]
	if w == nil || msg.gen != w.gen {
		return nil // invalidated meanwhile; a newer fetch is on its way
	}
	w.loading = false
	m.noteErr(msg.err)
	if msg.err != nil {
		return nil
	}
	w.days, w.paused, w.fetched = msg.days, msg.paused, m.svc.Now()
	m.clampCursors()
	return nil
}

// invalidate marks the week of date as outdated (after a write) and drops loads in flight.
func (m *model) invalidate(date time.Time) {
	if w := m.weeks[weekKey(date)]; w != nil {
		w.gen++
		w.loading = false
		w.fetched = time.Time{}
	}
}

// visible reports whether a day is listed: workdays always, other days only with entries
// (or while selected).
func (m *model) visible(d time.Time) bool {
	if m.svc.Cfg.IsWorkday(d) || d.Equal(m.date) {
		return true
	}
	w := m.weeks[weekKey(d)]
	if w == nil || w.days == nil {
		return false
	}
	day := w.days[int(d.Sub(service.Monday(d)).Hours()/24+0.5)]
	return len(day.Presences) > 0 || len(day.Activities) > 0
}

// stepDay moves the selection to the previous/next listed day, never past today.
func (m *model) stepDay(dir int) tea.Cmd {
	d := m.date
	for i := 0; i < 7; i++ {
		d = d.AddDate(0, 0, dir)
		if d.After(m.today()) {
			return nil
		}
		if m.visible(d) {
			return m.selectDate(d)
		}
	}
	return nil
}

// selectDate selects a day; a week that isn't cached is loaded once the navigation settles.
func (m *model) selectDate(d time.Time) tea.Cmd {
	if d.After(m.today()) {
		d = m.today()
	}
	if d.Equal(m.date) {
		return nil
	}
	m.date = d
	m.presCursor, m.actCursor = 0, 0
	return m.ensureWeek(true)
}

func (m *model) clampCursors() {
	d := m.selDay()
	if d == nil {
		return
	}
	m.presCursor = clamp(m.presCursor, 0, len(d.Presences)-1)
	m.actCursor = clamp(m.actCursor, 0, len(d.Activities)-1)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return max(lo, min(hi, v))
}
