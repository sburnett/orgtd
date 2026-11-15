package capturequeue

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sburnett/orgtd/internal/capture"
)

func titles(cs []capture.Capture) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Title)
	}
	return out
}

func TestAddUpdateDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "captures.json")
	if cs, err := Load(path); err != nil || len(cs) != 0 {
		t.Fatalf("Load(missing) = %v, %v", cs, err)
	}
	a, err := Add(path, capture.Capture{Title: "a"})
	if err != nil || a == "" {
		t.Fatalf("Add = %q, %v", a, err)
	}
	b, _ := Add(path, capture.Capture{Title: "b"})
	if _, err := Add(path, capture.Capture{ID: b, Title: "dup"}); err == nil {
		t.Error("Add with a queued ID succeeded")
	}
	if err := Update(path, capture.Capture{ID: a, Title: "a2"}); err != nil {
		t.Fatal(err)
	}
	cs, _ := Load(path)
	if got := titles(cs); len(got) != 2 || got[0] != "a2" || got[1] != "b" {
		t.Errorf("after update = %v", got)
	}
	if err := Delete(path, a); err != nil {
		t.Fatal(err)
	}
	if err := Delete(path, a); err == nil {
		t.Error("deleting twice succeeded")
	}
	if err := Update(path, capture.Capture{ID: a}); err == nil {
		t.Error("updating a deleted capture succeeded")
	}
	n, err := Remove(path, []string{b, "nope"})
	if n != 1 || err != nil {
		t.Errorf("Remove = %d, %v", n, err)
	}
}

func TestLoadToleratesMissingIDsUnknownFieldsAndDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "captures.json")
	os.WriteFile(path, []byte(`[
  {"title": "no id", "extra": 1, "createdAt": "2026-11-14T09:12:00-08:00"},
  {"id": "X", "title": "first"},
  {"id": "X", "title": "second"}
]`), 0o644)
	cs, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[0].ID == "" || cs[1].Title != "first" {
		t.Errorf("Load = %+v", cs)
	}
	if cs[0].CreatedAt.Hour() != 9 {
		t.Errorf("createdAt lost its wall time: %v", cs[0].CreatedAt)
	}
}

func TestImportMovesIncomingFilesIntoTheQueueInCaptureOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "captures.json")
	in := filepath.Join(dir, "incoming")
	os.Mkdir(in, 0o755)
	Add(path, capture.Capture{ID: "OLD", Title: "already queued"})
	os.WriteFile(filepath.Join(in, "b.json"), []byte(`{"title":"later","createdAt":"2026-11-14T10:00:00Z"}`), 0o644)
	os.WriteFile(filepath.Join(in, "a.json"), []byte(`{"id":"A","title":"earlier","createdAt":"2026-11-14T09:00:00Z"}`), 0o644)
	os.WriteFile(filepath.Join(in, "c.json"), []byte(`{"id":"OLD","title":"dup"}`), 0o644)
	os.WriteFile(filepath.Join(in, "bad.json"), []byte(`not json`), 0o644)

	added, err := Import(path, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(added); len(got) != 2 || got[0] != "earlier" || got[1] != "later" {
		t.Errorf("added = %v", got)
	}
	cs, _ := Load(path)
	if got := titles(cs); len(got) != 3 || got[0] != "already queued" {
		t.Errorf("queue = %v", got)
	}
	if cs[2].ID != "B" {
		t.Errorf("ID from file name = %q, want B", cs[2].ID)
	}
	left, _ := os.ReadDir(in)
	if len(left) != 1 || left[0].Name() != "bad.json.bad" {
		t.Errorf("incoming dir left with %v", left)
	}
}
