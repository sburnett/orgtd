package workspace

import (
	"testing"

	"github.com/sburnett/orgtd/internal/org"
)

func twoFileWorkspace() (*Workspace, *org.File, *org.File) {
	child := &org.Headline{Level: 2, Title: "child"}
	a1 := &org.Headline{Level: 1, Title: "a1", Children: []*org.Headline{child}}
	child.Parent = a1
	a2 := &org.Headline{Level: 1, Title: "a2"}
	b1 := &org.Headline{Level: 1, Title: "b1"}
	fa := &org.File{Path: "a.org", Headlines: []*org.Headline{a1, a2}}
	fb := &org.File{Path: "b.org", Headlines: []*org.Headline{b1}}
	return &Workspace{Files: []*org.File{fa, fb}}, fa, fb
}

func TestFileOfFindsTheFileForTopLevelAndNestedHeadlines(t *testing.T) {
	ws, fa, fb := twoFileWorkspace()
	if got := ws.FileOf(fa.Headlines[1]); got != fa {
		t.Errorf("FileOf(a2) = %v, want a.org", got)
	}
	if got := ws.FileOf(fa.Headlines[0].Children[0]); got != fa {
		t.Errorf("FileOf(nested child of a1) = %v, want a.org", got)
	}
	if got := ws.FileOf(fb.Headlines[0]); got != fb {
		t.Errorf("FileOf(b1) = %v, want b.org", got)
	}
}

func TestFileOfIsNilForAHeadlineInNoLoadedFile(t *testing.T) {
	ws, _, _ := twoFileWorkspace()
	if got := ws.FileOf(&org.Headline{Level: 1, Title: "orphan"}); got != nil {
		t.Errorf("FileOf(orphan) = %v, want nil", got)
	}
}

func TestLocateReturnsFileParentAndIndex(t *testing.T) {
	ws, fa, _ := twoFileWorkspace()
	a1 := fa.Headlines[0]

	if f, parent, idx := ws.Locate(fa.Headlines[1]); f != fa || parent != nil || idx != 1 {
		t.Errorf("Locate(a2) = (%v, %v, %d), want (a.org, nil, 1)", f, parent, idx)
	}
	if f, parent, idx := ws.Locate(a1.Children[0]); f != fa || parent != a1 || idx != 0 {
		t.Errorf("Locate(child) = (%v, %v, %d), want (a.org, a1, 0)", f, parent, idx)
	}
}

// A headline whose file can't be found must not crash Locate (the bug
// that once took down every edit session on a stale reference): a
// top-level one has nowhere to be looked up, a nested one is still found
// through its parent.
func TestLocateToleratesDetachedHeadlines(t *testing.T) {
	ws, _, _ := twoFileWorkspace()

	orphan := &org.Headline{Level: 1, Title: "orphan"}
	if f, parent, idx := ws.Locate(orphan); f != nil || parent != nil || idx != -1 {
		t.Errorf("Locate(orphan) = (%v, %v, %d), want (nil, nil, -1)", f, parent, idx)
	}

	parent := &org.Headline{Level: 1, Title: "detached parent"}
	child := &org.Headline{Level: 2, Title: "detached child", Parent: parent}
	parent.Children = []*org.Headline{child}
	if f, p, idx := ws.Locate(child); f != nil || p != parent || idx != 0 {
		t.Errorf("Locate(detached child) = (%v, %v, %d), want (nil, parent, 0)", f, p, idx)
	}
}
