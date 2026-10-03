package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/sburnett/orgtd/internal/org"
)

// fixedClock returns a WithClock option pinning "now" to t.
func fixedClock(t time.Time) Option {
	return WithClock(func() time.Time { return t })
}

// A moment that has nothing to do with when the tests actually run.
var pinned = time.Date(2031, 3, 14, 15, 9, 0, 0, time.Local)

func TestWithClockPinsTheAgendaBuckets(t *testing.T) {
	ws := agendaFixture(t, `* TODO Yesterday's thing
  SCHEDULED: <2031-03-13 Thu>
* TODO Today's thing
  SCHEDULED: <2031-03-14 Fri>
* TODO Next week's thing
  SCHEDULED: <2031-03-20 Thu>
`)
	m := New(ws, fixedClock(pinned))
	m.switchToView(agendaView)

	section := ""
	got := map[string]string{}
	for _, r := range m.rows {
		switch {
		case r.kind == rowSection:
			section = r.text
		case r.headline != nil:
			got[r.headline.Title] = section
		}
	}
	want := map[string]string{"Yesterday's thing": "Overdue", "Today's thing": "Due Today", "Next week's thing": "Upcoming"}
	for title, sec := range want {
		if got[title] != sec {
			t.Errorf("%q is under %q, want %q (clock pinned to %s)", title, got[title], sec, pinned.Format("2006-01-02"))
		}
	}
}

func TestWithClockStampsClosedWhenMarkingDone(t *testing.T) {
	ws := agendaFixture(t, "* TODO Finish the report\n")
	m := New(ws, fixedClock(pinned))
	m.cursor = findRow(t, m, "Finish the report")
	h := m.currentHeadline()

	m = setStatus(m, "d") // DONE

	if h.Closed == nil || !strings.Contains(h.Closed.Raw, "2031-03-14 Fri 15:09") {
		t.Errorf("CLOSED = %v, want it stamped from the pinned clock (2031-03-14 Fri 15:09)", h.Closed)
	}
}

func TestWithClockResolvesRelativeDeadlinePhrases(t *testing.T) {
	ws := agendaFixture(t, "* TODO Plan the trip\n")
	m := New(ws, fixedClock(pinned))
	m.cursor = findRow(t, m, "Plan the trip")
	h := m.currentHeadline()

	m = sendKey(m, "g")
	m = sendKey(m, "d")
	m = typeKeys(m, "2w")
	m = sendKey(m, "enter")

	if want := "2031-03-28 Fri"; h.Deadline == nil || h.Deadline.Raw != want {
		t.Errorf("deadline = %v, want %q (two weeks after the pinned date)", h.Deadline, want)
	}
}

func TestWithClockDecidesWhichMeetingIsInProgress(t *testing.T) {
	// The same calendar, viewed at two different pinned moments, lands the
	// calendar view's cursor on different meetings.
	morning := time.Date(2031, 3, 14, 9, 30, 0, 0, time.Local)
	afternoon := time.Date(2031, 3, 14, 14, 30, 0, 0, time.Local)
	build := func() *org.File {
		return &org.File{Path: "calendar.org", Headlines: []*org.Headline{
			oneOffCalendarEventHeadline("am", "Morning sync", time.Date(2031, 3, 14, 9, 0, 0, 0, time.Local), time.Date(2031, 3, 14, 10, 0, 0, 0, time.Local)),
			oneOffCalendarEventHeadline("pm", "Afternoon sync", time.Date(2031, 3, 14, 14, 0, 0, 0, time.Local), time.Date(2031, 3, 14, 15, 0, 0, 0, time.Local)),
		}}
	}
	for _, c := range []struct {
		at   time.Time
		want string
	}{{morning, "Morning sync"}, {afternoon, "Afternoon sync"}} {
		m := New(meetingsFixture(build()), fixedClock(c.at))
		m.enterCalendarView()
		if h := m.currentHeadline(); h == nil || h.Title != c.want {
			t.Errorf("at %s the calendar cursor is on %v, want %q", c.at.Format("15:04"), h, c.want)
		}
	}
}

func TestNowFallsBackToTheRealClock(t *testing.T) {
	m := New(agendaFixture(t, "* TODO x\n"))
	before := time.Now()
	got := m.now()
	if got.Before(before) || time.Since(got) > time.Minute {
		t.Errorf("now() = %v with no clock set, want roughly the real time", got)
	}
}
