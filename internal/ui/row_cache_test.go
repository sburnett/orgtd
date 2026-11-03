package ui

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sburnett/orgtd/internal/workspace"
)

// loadDemoCopy loads a scratch copy of testdata/orgdir, reference/
// included, for tests that may edit it.
func loadDemoCopy(t *testing.T) *workspace.Workspace {
	t.Helper()
	dst := t.TempDir()
	for _, sub := range []string{"", workspace.ReferenceDir} {
		src := filepath.Join("../../testdata/orgdir", sub)
		entries, err := os.ReadDir(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dst, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".org" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dst, sub, e.Name()), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	ws, err := workspace.Load(dst)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// sameRows reports whether two row builds are identical, comparing a
// wrapped body line's segments rather than the pointer holding them.
func sameRows(a, b []row) (bool, string) {
	if len(a) != len(b) {
		return false, fmt.Sprintf("%d rows, fresh build has %d", len(a), len(b))
	}
	for i := range a {
		x, y := a[i], b[i]
		var xs, ys []string
		if x.wrap != nil {
			xs = x.wrap.segs
		}
		if y.wrap != nil {
			ys = y.wrap.segs
		}
		x.wrap, y.wrap = nil, nil
		if x != y || !slices.Equal(xs, ys) {
			return false, fmt.Sprintf("row %d differs:\n  have %+v\n  want %+v", i, a[i], b[i])
		}
	}
	return true, ""
}

// checkWindowCaches checks the window currently swapped into m: its rows,
// and what searchRows returns (cached or not), must match a fresh build
// from the workspace as it stands now (with a fresh meeting index, too).
func checkWindowCaches(t *testing.T, m *Model, where string) {
	t.Helper()
	fresh := *m
	fresh.meetings = nil // build an uncached meeting index
	spec := fresh.spec()
	if !spec.static {
		var rows []row
		spec.build(&fresh, &rows, false)
		if ok, why := sameRows(m.rows, rows); !ok {
			t.Fatalf("%s: %v window's rows are stale: %s", where, m.view, why)
		}
	}
	if spec.folds {
		var rows []row
		spec.build(&fresh, &rows, true)
		if ok, why := sameRows(m.searchRows(), rows); !ok {
			t.Fatalf("%s: %v window's cached search rows are stale: %s", where, m.view, why)
		}
	}
}

// TestRowCachesNeverGoStale drives the app through long random runs of
// keys and commands — edits, folds, undo, views, splits, resizes, marks,
// search, and the clock moving on — rendering after each like the real
// program does (which fills the search cache), and checks every window's
// rows and cached search rows against a fresh build after every step.
func TestRowCachesNeverGoStale(t *testing.T) {
	if testing.Short() {
		t.Skip("long randomized run")
	}
	key := func(k string) tea.Msg { return keyMsgFor(k) }
	ctrl := func(t tea.KeyType) tea.Msg { return tea.KeyMsg{Type: t} }
	type action []tea.Msg
	keys := func(ks ...string) action {
		var a action
		for _, k := range ks {
			a = append(a, key(k))
		}
		return a
	}
	colon := func(cmd string) action {
		a := action{key(":")}
		for _, r := range cmd {
			a = append(a, key(string(r)))
		}
		return append(a, key("enter"))
	}
	actions := []action{
		keys("j"), keys("j"), keys("j"), keys("k"), keys("k"), keys("J"), keys("K"),
		keys("}"), keys("{"), keys("h"), keys("l"), keys("g", "g"), keys("G"),
		{ctrl(tea.KeyCtrlD)}, {ctrl(tea.KeyCtrlU)}, {ctrl(tea.KeyPgDown)}, {ctrl(tea.KeyPgUp)},
		{ctrl(tea.KeyCtrlE)}, {ctrl(tea.KeyCtrlY)}, {ctrl(tea.KeyCtrlO)},
		keys("z", "a"), keys("z", "c"), keys("z", "o"), keys("z", "O"), keys("z", "C"), keys("z", "A"),
		keys("tab"), keys("z", "R"), keys("z", "M"),
		keys("d", "d"), keys("y", "y"), keys("p"), keys("P"), keys(">", ">"), keys("<", "<"),
		keys("u"), keys("u"), {ctrl(tea.KeyCtrlR)},
		keys("r", "t"), keys("r", "d"), keys("r", "n"), keys("R", "d"),
		keys("g", "d", "t", "o", "m", "o", "r", "r", "o", "w", "enter"),
		keys("g", "t", "x", "y", "enter"),
		keys("m", "a"), keys("'", "a"), keys("m", "b"),
		keys("V", "j", "j", "esc"), keys("V", "j", "d"), keys("V", "j", ">"),
		keys("/", "e", "enter"), keys("/", "m", "e", "e", "t"), keys("esc"), keys("n"), keys("N"),
		colon("split"), colon("vsplit"), colon("close"), colon("only"),
		{ctrl(tea.KeyCtrlW), key("w")}, {ctrl(tea.KeyCtrlW), key("p")},
		colon("outline"), colon("outline"), colon("agenda"), colon("calendar"), colon("tags"),
		colon("snippets"), colon("reference"), colon("meeting-tags"), colon("config"),
		colon("toggleclosed"), colon("review"), colon("next"), colon("prev"),
		keys("enter"),
		{tea.WindowSizeMsg{Width: 60, Height: 30}}, {tea.WindowSizeMsg{Width: 140, Height: 45}},
		{tea.WindowSizeMsg{Width: 90, Height: 20}},
	}

	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			now := demoNow
			m := New(loadDemoCopy(t), WithClock(func() time.Time { return now }))
			next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
			m = next.(Model)
			_ = m.View()
			var history []string
			for step := 0; step < 200; step++ {
				var a action
				if rng.Intn(15) == 0 {
					// Let time pass: a few minutes to most of a day.
					now = now.Add(time.Duration(rng.Intn(18*60)+1) * time.Minute)
					history = append(history, "clock→"+now.Format("01-02 15:04"))
					a = action{key("j")} // any key, so the app sees the new time
				} else {
					a = actions[rng.Intn(len(actions))]
				}
				for _, msg := range a {
					if km, ok := msg.(tea.KeyMsg); ok {
						history = append(history, km.String())
					} else {
						history = append(history, fmt.Sprintf("%T%+v", msg, msg))
					}
					next, _ := m.Update(msg)
					m = next.(Model)
				}
				where := fmt.Sprintf("after %v", history[max(0, len(history)-25):])
				// The active window itself only rebuilds when something
				// changes, so after the clock moves its rows (and the
				// matching cache) may lag a fresh build until then — the
				// inactive windows must not.
				if len(history) > 0 && !timeMovedSinceRebuild(m) {
					checkWindowCaches(t, &m, where+" (active window)")
				}
				for _, p := range m.panes() {
					if p.leaf != m.activeWin {
						m.withWindow(p, func() { checkWindowCaches(t, &m, where+" (inactive window)") })
					}
				}
				_ = m.View()
			}
		})
	}
}

// timeMovedSinceRebuild reports whether the clock has moved on since the
// active window last built its rows.
func timeMovedSinceRebuild(m Model) bool {
	return m.builtAt != m.minute()
}
