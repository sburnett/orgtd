package ui

import (
	"strings"
	"testing"

	"github.com/sburnett/orgtd/internal/org"
	"github.com/sburnett/orgtd/internal/workspace"
)

// writeBatchFakeFormatter writes a shell script suitable as a batch
// urlFormatterCmd: it reads lines from stdin and, for each, echoes the
// result of running body's shell fragment with $line bound to it.
func writeBatchFakeFormatter(t *testing.T, body string) string {
	t.Helper()
	return writeFakeFormatter(t, "while IFS= read -r line; do\n"+body+"\ndone")
}

func TestCollectFormatLinksTargetsFindsBareURLsAndSkipsFormattedOnes(t *testing.T) {
	ws := agendaFixture(t, strings.Join([]string{
		"* TODO Has a bare url https://example.com/a here",
		"* TODO Already linked [[https://example.com/b][B]]",
		"* TODO No url at all",
	}, "\n")+"\n")
	m := New(ws, WithURLFormatter("fake"))

	targets, urls := m.collectFormatLinksTargets()

	if len(targets) != 1 {
		t.Fatalf("targets = %d, want 1 (only the bare-url entry)", len(targets))
	}
	if targets[0].h.Title != "Has a bare url https://example.com/a here" {
		t.Errorf("target = %q", targets[0].h.Title)
	}
	if len(urls) != 1 || urls[0] != "https://example.com/a" {
		t.Errorf("urls = %v, want [https://example.com/a]", urls)
	}
}

// TestCollectFormatLinksTargetsSkipsLinkWithBracketedDescription guards
// the actual bug report: a link whose description contains a bracket
// (e.g. a formatter's own earlier output for a page titled "Bracket
// [disambiguation]") is valid org-mode syntax, but used to fail our own
// orgLinkRe match entirely — so :format-links would treat its url as
// still bare and re-run it through the formatter every time, expanding
// it again on top of itself.
func TestCollectFormatLinksTargetsSkipsLinkWithBracketedDescription(t *testing.T) {
	ws := agendaFixture(t, "* TODO Already linked [[https://example.com/a][Bracket [disambiguation] title]]\n")
	m := New(ws, WithURLFormatter("fake"))

	targets, urls := m.collectFormatLinksTargets()

	if len(targets) != 0 || len(urls) != 0 {
		t.Errorf("targets = %v, urls = %v, want none (the url is already inside a valid link)", targets, urls)
	}
}

func TestCollectFormatLinksTargetsScansBodyLinesToo(t *testing.T) {
	ws := agendaFixture(t, "* TODO No url in the title\n  A body line with https://example.com/b in it.\n")
	m := New(ws, WithURLFormatter("fake"))

	targets, urls := m.collectFormatLinksTargets()

	if len(targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(targets))
	}
	if len(urls) != 1 || urls[0] != "https://example.com/b" {
		t.Errorf("urls = %v, want [https://example.com/b]", urls)
	}
	if targets[0].spans[0].field == formatLinksTitleField {
		t.Errorf("span field = title, want the body line's index")
	}
}

func TestCollectFormatLinksTargetsOrdersMultipleURLsInOneField(t *testing.T) {
	ws := agendaFixture(t, "* TODO See https://example.com/a and https://example.com/b\n")
	m := New(ws, WithURLFormatter("fake"))

	_, urls := m.collectFormatLinksTargets()

	want := []string{"https://example.com/a", "https://example.com/b"}
	if len(urls) != len(want) || urls[0] != want[0] || urls[1] != want[1] {
		t.Errorf("urls = %v, want %v (in left-to-right order)", urls, want)
	}
}

func TestCollectFormatLinksTargetsSkipsAlreadyLockedHeadlines(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("fake"))
	h := findHeadlineByTitle(t, m, "url https://example.com/a")
	m.immutable[h] = true

	targets, urls := m.collectFormatLinksTargets()

	if len(targets) != 0 || len(urls) != 0 {
		t.Errorf("targets = %v, urls = %v, want none (already locked by an earlier batch)", targets, urls)
	}
}

func TestStartFormatLinksLocksTargetsAndSetsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n* TODO plain\n")
	m := New(ws, WithURLFormatter("fake"))
	h := findHeadlineByTitle(t, m, "url https://example.com/a")

	cmd := m.startFormatLinks()

	if cmd == nil {
		t.Fatal("expected a non-nil command")
	}
	if !m.immutable[h] {
		t.Error("targeted headline should be locked")
	}
	if !strings.Contains(m.message, "1 entries") && !strings.Contains(m.message, "1 entrie") {
		t.Errorf("message = %q, want it to mention 1 entry", m.message)
	}
}

func TestFormatLinksFormatterCmdFallsBackToURLFormatter(t *testing.T) {
	m := New(agendaFixture(t, "* TODO x\n"), WithURLFormatter("live-editor-formatter"))
	if got := m.formatLinksFormatterCmd(); got != "live-editor-formatter" {
		t.Errorf("formatLinksFormatterCmd() = %q, want it to fall back to WithURLFormatter's value", got)
	}
}

func TestFormatLinksFormatterCmdUsesItsOwnSettingWhenSet(t *testing.T) {
	m := New(agendaFixture(t, "* TODO x\n"),
		WithURLFormatter("live-editor-formatter"),
		WithFormatLinksURLFormatter("batch-formatter"),
	)
	if got := m.formatLinksFormatterCmd(); got != "batch-formatter" {
		t.Errorf("formatLinksFormatterCmd() = %q, want its own configured value, not the live-editor one", got)
	}
}

func TestStartFormatLinksInvokesTheFormatLinksSpecificFormatter(t *testing.T) {
	// The batch formatter script echoes marker text back so we can tell
	// which of the two configured commands actually ran.
	batchScript := writeBatchFakeFormatter(t, `echo "[[$line][FromBatchFormatter]]"`)
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws,
		WithURLFormatter("/no/such/live-editor-formatter"), // would fail if ever invoked
		WithFormatLinksURLFormatter(batchScript),
	)
	h := findHeadlineByTitle(t, m, "url https://example.com/a")

	cmd := m.startFormatLinks()
	msg := cmd().(formatLinksMsg)
	if msg.err != nil {
		t.Fatalf("batch formatting failed: %v (should have used the format-links-specific formatter, not url_formatter)", msg.err)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)

	if h.Title != "url [[https://example.com/a][FromBatchFormatter]]" {
		t.Errorf("title = %q, want it formatted by the format-links-specific formatter", h.Title)
	}
}

func TestFormatLinksCommandNoFormatterConfiguredShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws) // no WithURLFormatter

	m = sendKey(m, ":")
	m = typeKeys(m, "format-links")
	m, cmd := sendKeyCmd(m, "enter")

	if cmd != nil {
		t.Error("expected no command when no formatter is configured")
	}
	if !strings.Contains(m.message, "No URL formatter configured") {
		t.Errorf("message = %q", m.message)
	}
}

func TestFormatLinksCommandNoUnformattedURLsShowsMessage(t *testing.T) {
	ws := agendaFixture(t, "* TODO No urls here\n")
	m := New(ws, WithURLFormatter("fake"))

	m = sendKey(m, ":")
	m = typeKeys(m, "format-links")
	m, cmd := sendKeyCmd(m, "enter")

	if cmd != nil {
		t.Error("expected no command when there's nothing to format")
	}
	if !strings.Contains(m.message, "No unformatted URLs found") {
		t.Errorf("message = %q", m.message)
	}
}

func TestImmutableEntryGutterShowsLockIndicator(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()

	idx := findRow(t, m, "url https://example.com/a")
	line := stripANSI(m.renderRow(m.rows[idx]))
	if !strings.Contains(line, "◆") {
		t.Errorf("locked row = %q, want the ◆ lock indicator", line)
	}
}

func TestImmutableEntryRendersFaint(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO Second\n")
	m := New(ws, WithURLFormatter("fake"))
	m.width = 60

	lockedIdx := findRow(t, m, "First")
	unlockedIdx := findRow(t, m, "Second")

	before := m.renderRow(m.rows[lockedIdx])
	if strings.Contains(before, "\x1b[2") {
		t.Fatalf("row should not already render faint before locking: %q", before)
	}

	h := findHeadlineByTitle(t, m, "First")
	m.immutable[h] = true

	locked := m.renderRow(m.rows[lockedIdx])
	unlocked := m.renderRow(m.rows[unlockedIdx])

	if !strings.Contains(locked, "\x1b[2") {
		t.Errorf("locked row should render with the faint (dim) SGR attribute: %q", locked)
	}
	if strings.Contains(unlocked, "\x1b[2") {
		t.Errorf("unlocked row should not render faint: %q", unlocked)
	}
}

func TestImmutableEntryRefusesDelete(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n* TODO other\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()
	m.cursor = findRow(t, m, "url https://example.com/a")

	m = sendKey(m, "d")
	m = sendKey(m, "d")

	if rowIndex(m, "url https://example.com/a") < 0 {
		t.Error("locked entry should not have been deleted")
	}
	if !strings.Contains(m.message, "format-links") {
		t.Errorf("message = %q, want it to explain the entry is locked", m.message)
	}
}

func TestImmutableEntryRefusesStatusChange(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()
	m.cursor = findRow(t, m, "url https://example.com/a")
	h := m.currentHeadline()

	m = sendKey(m, "r")

	if h.Keyword != "TODO" {
		t.Errorf("keyword = %q, want unchanged TODO", h.Keyword)
	}
}

func TestImmutableEntryRefusesDeadlineChange(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()
	m.cursor = findRow(t, m, "url https://example.com/a")

	m = sendKey(m, "g")
	m = sendKey(m, "d")

	if m.mode == deadlineMode {
		t.Error("deadline prompt should not open for a locked entry")
	}
}

func TestImmutableEntryRefusesEdit(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()
	m.cursor = findRow(t, m, "url https://example.com/a")

	_, cmd := sendKeyCmd(m, "i")

	if cmd != nil {
		t.Error("expected no edit command for a locked entry")
	}
}

func TestImmutableEntryRefusesPromoteAndDemote(t *testing.T) {
	ws := agendaFixture(t, "* TODO First\n* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()
	m.cursor = findRow(t, m, "url https://example.com/a")
	h := m.currentHeadline()
	origLevel := h.Level

	m = sendKey(m, ">")
	m = sendKey(m, ">")
	if h.Level != origLevel {
		t.Errorf("level after >> on a locked entry = %d, want unchanged %d", h.Level, origLevel)
	}

	m = sendKey(m, "<")
	m = sendKey(m, "<")
	if h.Level != origLevel {
		t.Errorf("level after << on a locked entry = %d, want unchanged %d", h.Level, origLevel)
	}
}

func TestImmutableEntryRefusesWholeFileEdit(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()
	m.cursor = 0 // the file header row

	m, cmd := sendKeyCmd(m, "i")

	if cmd != nil || m.mode == confirmMode {
		t.Error("whole-file edit should be refused outright, not even prompt for confirmation")
	}
	if !strings.Contains(m.message, "format-links") {
		t.Errorf("message = %q, want it to mention format-links", m.message)
	}
}

func TestBulkDeleteSkipsImmutableEntries(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n* TODO plain\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()
	m.cursor = findRow(t, m, "url https://example.com/a")

	m = sendKey(m, "V")
	m = sendKey(m, "j")
	m = sendKey(m, "d")

	if rowIndex(m, "url https://example.com/a") < 0 {
		t.Error("locked entry should have survived the bulk delete")
	}
	if rowIndex(m, "plain") >= 0 {
		t.Error("unlocked entry should have been deleted")
	}
	if !strings.Contains(m.message, "skipped") {
		t.Errorf("message = %q, want it to mention a skipped entry", m.message)
	}
}

func TestBulkStatusChangeSkipsImmutableEntries(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n* TODO plain\n")
	m := New(ws, WithURLFormatter("fake"))
	m.startFormatLinks()
	lockedHeadline := findHeadlineByTitle(t, m, "url https://example.com/a")
	m.cursor = findRow(t, m, "url https://example.com/a")

	m = sendKey(m, "V")
	m = sendKey(m, "j")
	m = sendKey(m, "R")
	m = sendKey(m, "d") // DONE

	if lockedHeadline.Keyword != "TODO" {
		t.Errorf("locked entry keyword = %q, want unchanged TODO", lockedHeadline.Keyword)
	}
	if h := findHeadlineByTitle(t, m, "plain"); h.Keyword != "DONE" {
		t.Errorf("unlocked entry keyword = %q, want DONE", h.Keyword)
	}
	if !strings.Contains(m.message, "skipped") {
		t.Errorf("message = %q, want it to mention a skipped entry", m.message)
	}
}

func TestUndoRefusedWhenLastActionTouchesImmutableEntry(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("fake"))
	m.cursor = findRow(t, m, "url https://example.com/a")
	m = sendKey(m, "r") // TODO -> NEXT, pushes an undoable action
	h := m.currentHeadline()
	if h.Keyword != "NEXT" {
		t.Fatalf("fixture assumption broken: keyword = %q", h.Keyword)
	}

	m.startFormatLinks() // locks h without pushing any new undo action

	m = sendKey(m, "u")

	if h.Keyword != "NEXT" {
		t.Errorf("keyword after refused undo = %q, want unchanged NEXT", h.Keyword)
	}
	if !strings.Contains(m.message, "format-links") {
		t.Errorf("message = %q, want it to explain why undo was refused", m.message)
	}
}

func TestRunBatchURLFormatterMatchesLinesByIndex(t *testing.T) {
	script := writeBatchFakeFormatter(t, `echo "[[$line][Formatted]]"`)
	got, err := runBatchURLFormatter(script, []string{"https://a.example.com", "https://b.example.com"})
	if err != nil {
		t.Fatalf("runBatchURLFormatter: %v", err)
	}
	want := []string{"[[https://a.example.com][Formatted]]", "[[https://b.example.com][Formatted]]"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRunBatchURLFormatterErrorsOnLineCountMismatch(t *testing.T) {
	script := writeFakeFormatter(t, `echo "only one line"`)
	_, err := runBatchURLFormatter(script, []string{"https://a.example.com", "https://b.example.com"})
	if err == nil {
		t.Fatal("expected an error when the formatter's output line count doesn't match the input")
	}
}

func TestRunBatchURLFormatterErrorsOnFailure(t *testing.T) {
	script := writeFakeFormatter(t, `echo "boom" >&2; exit 1`)
	_, err := runBatchURLFormatter(script, []string{"https://a.example.com"})
	if err == nil {
		t.Fatal("expected an error when the formatter process fails")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to include the subprocess's stderr", err)
	}
}

func TestFinishFormatLinksAppliesFormattedTextAndUnlocks(t *testing.T) {
	script := writeBatchFakeFormatter(t, `echo "[[$line][Formatted]]"`)
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter(script))
	h := findHeadlineByTitle(t, m, "url https://example.com/a")

	cmd := m.startFormatLinks()
	msg := cmd().(formatLinksMsg)
	updated, _ := m.Update(msg)
	m = updated.(Model)

	if m.immutable[h] {
		t.Error("entry should be unlocked after the batch finishes")
	}
	if h.Title != "url [[https://example.com/a][Formatted]]" {
		t.Errorf("title = %q", h.Title)
	}
	if !strings.Contains(m.message, "Formatted links for 1 entries") {
		t.Errorf("message = %q", m.message)
	}
}

func TestFinishFormatLinksHandlesMultipleURLsInOneEntry(t *testing.T) {
	script := writeBatchFakeFormatter(t, `echo "[[$line][Formatted]]"`)
	ws := agendaFixture(t, "* TODO See https://example.com/a and https://example.com/b\n")
	m := New(ws, WithURLFormatter(script))
	h := findHeadlineByTitle(t, m, "See https://example.com/a and https://example.com/b")

	cmd := m.startFormatLinks()
	msg := cmd().(formatLinksMsg)
	updated, _ := m.Update(msg)
	m = updated.(Model)

	want := "See [[https://example.com/a][Formatted]] and [[https://example.com/b][Formatted]]"
	if h.Title != want {
		t.Errorf("title = %q, want %q", h.Title, want)
	}
}

func TestFinishFormatLinksRewritesBodyLines(t *testing.T) {
	script := writeBatchFakeFormatter(t, `echo "[[$line][Formatted]]"`)
	ws := agendaFixture(t, "* TODO No url in title\n  See https://example.com/a here.\n")
	m := New(ws, WithURLFormatter(script))
	h := findHeadlineByTitle(t, m, "No url in title")

	cmd := m.startFormatLinks()
	msg := cmd().(formatLinksMsg)
	updated, _ := m.Update(msg)
	m = updated.(Model)

	want := "  See [[https://example.com/a][Formatted]] here."
	if len(h.Body) != 1 || h.Body[0] != want {
		t.Errorf("body = %#v, want [%q]", h.Body, want)
	}
}

func TestFinishFormatLinksOnFailureUnlocksWithoutChangingContent(t *testing.T) {
	ws := agendaFixture(t, "* TODO url https://example.com/a\n")
	m := New(ws, WithURLFormatter("/no/such/program/anywhere"))
	h := findHeadlineByTitle(t, m, "url https://example.com/a")

	cmd := m.startFormatLinks()
	msg := cmd().(formatLinksMsg)
	if msg.err == nil {
		t.Fatal("expected the batch to fail (no such program)")
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)

	if m.immutable[h] {
		t.Error("entry should be unlocked even after a failed batch")
	}
	if h.Title != "url https://example.com/a" {
		t.Errorf("title = %q, want unchanged", h.Title)
	}
	if !strings.Contains(m.message, "Link formatting failed") {
		t.Errorf("message = %q", m.message)
	}
}

func TestFinishFormatLinksIsOneUndoStepPerFile(t *testing.T) {
	fileA, err := org.Parse(strings.NewReader("* TODO A https://example.com/a\n"), "a.org")
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	fileB, err := org.Parse(strings.NewReader("* TODO B https://example.com/b\n"), "b.org")
	if err != nil {
		t.Fatalf("org.Parse: %v", err)
	}
	ws := &workspace.Workspace{Dir: "multi-file-fixture", Files: []*org.File{fileA, fileB}}
	script := writeBatchFakeFormatter(t, `echo "[[$line][Formatted]]"`)
	m := New(ws, WithURLFormatter(script))
	hA := fileA.Headlines[0]
	hB := fileB.Headlines[0]

	undoDepthBefore := m.undoPos
	cmd := m.startFormatLinks()
	msg := cmd().(formatLinksMsg)
	updated, _ := m.Update(msg)
	m = updated.(Model)

	if got := m.undoPos - undoDepthBefore; got != 2 {
		t.Errorf("undo steps = %d, want 2 (one per file touched)", got)
	}
	if hA.Title != "A [[https://example.com/a][Formatted]]" || hB.Title != "B [[https://example.com/b][Formatted]]" {
		t.Fatalf("titles after formatting = %q, %q", hA.Title, hB.Title)
	}

	m.undo()
	aFormatted := hA.Title != "A https://example.com/a"
	bFormatted := hB.Title != "B https://example.com/b"
	if aFormatted == bFormatted {
		t.Errorf("one undo should revert exactly one file's change, got A formatted=%v, B formatted=%v", aFormatted, bFormatted)
	}
}
