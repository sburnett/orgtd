package ui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/org"
)

// TestDirtyMeansDiffersFromDisk runs random sequences of edits, undo,
// redo and :w, and after every step checks that each file whose
// in-memory content differs from what's on disk reads as unsaved — so
// :w never says "No changes to write" while there are changes to write.
//
// "What's on disk" is the disk file parsed and rendered the same way
// the in-memory file is: RenderFile isn't a byte-for-byte round trip
// (see its doc comment), so comparing against the raw bytes would flag
// a file orgtd never touched.
//
// The converse (unsaved means differs) is deliberately not checked:
// dirtiness is tracked from undo history, so an edit followed by a
// separate edit that happens to restore the old content still reads as
// unsaved, which is harmless — :w just writes an identical file.
func TestDirtyMeansDiffersFromDisk(t *testing.T) {
	const runs, steps = 20, 40
	for seed := int64(1); seed <= runs; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			t.Parallel()
			runDirtyProperty(t, seed, steps)
		})
	}
}

func runDirtyProperty(t *testing.T, seed int64, steps int) {
	rng := rand.New(rand.NewSource(seed))
	m := New(loadFixtureCopy(t))

	var history []string
	check := func() {
		t.Helper()
		for _, f := range m.ws.Files {
			disk, err := org.ParseFile(f.Path)
			if err != nil {
				t.Fatalf("ParseFile(%s): %v", f.Path, err)
			}
			want, got := org.RenderFile(disk), org.RenderFile(f)
			if got != want && !m.unsaved(f) {
				t.Fatalf("seed %d: %s differs from disk but reads as saved, after:\n  %s\n--- disk ---\n%s--- memory ---\n%s",
					seed, f.Path, strings.Join(history, "\n  "), want, got)
			}
		}
	}

	shortcuts := []string{}
	for _, c := range m.statusCandidates() {
		shortcuts = append(shortcuts, string(c.shortcut))
	}

	inserted := 0
	check()
	for i := 0; i < steps; i++ {
		// Every edit acts on a random headline row; undo, redo and :w
		// don't care where the cursor is.
		rows := headlineRows(m)
		if len(rows) == 0 {
			break
		}
		m.cursor = rows[rng.Intn(len(rows))]
		title := m.currentHeadline().Title

		var op string
		switch r := rng.Intn(100); {
		case r < 20:
			sc := shortcuts[rng.Intn(len(shortcuts))]
			op = fmt.Sprintf("status %q on %q", sc, title)
			m = setStatus(m, sc)
		case r < 28:
			op = fmt.Sprintf("dd on %q", title)
			m = sendKey(sendKey(m, "d"), "d")
		case r < 36:
			inserted++
			body := fmt.Sprintf("TODO Inserted %d\n", inserted)
			op = fmt.Sprintf("o %q after %q", strings.TrimSpace(body), title)
			m = sendKey(m, "o")
			m = finishInsertWith(t, m, body)
		case r < 42:
			op = fmt.Sprintf("yy p on %q", title)
			m = sendKey(sendKey(sendKey(m, "y"), "y"), "p")
		case r < 48:
			op = fmt.Sprintf(">> on %q", title)
			m = sendKey(sendKey(m, ">"), ">")
		case r < 54:
			op = fmt.Sprintf("<< on %q", title)
			m = sendKey(sendKey(m, "<"), "<")
		case r < 75:
			op = "u"
			m = sendKey(m, "u")
		case r < 92:
			op = "ctrl+r"
			m = sendKey(m, "ctrl+r")
		default:
			op = ":w"
			m = saveAll(m)
		}
		if m.mode != normalMode {
			t.Fatalf("seed %d: %s left mode %d, after:\n  %s", seed, op, m.mode, strings.Join(history, "\n  "))
		}
		history = append(history, op)
		check()
	}
}

// headlineRows returns the index of every row showing a headline (not
// a file row or a body line).
func headlineRows(m Model) []int {
	var out []int
	for i, r := range m.rows {
		if r.headline != nil && r.kind != rowBody {
			out = append(out, i)
		}
	}
	return out
}
