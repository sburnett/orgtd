package orgtd

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestDesignListsEveryUIFile keeps the file table in DESIGN.md's "The UI
// package" section in step with internal/ui: every non-test .go file
// there must be named in the table, and every file the table names must
// exist. Adding, renaming or removing a file without updating the table
// fails here.
func TestDesignListsEveryUIFile(t *testing.T) {
	data, err := os.ReadFile("DESIGN.md")
	if err != nil {
		t.Fatal(err)
	}
	table := uiFileTable(t, string(data))

	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("`([a-z0-9_]+\\.go)`").FindAllStringSubmatch(table, -1) {
		documented[m[1]] = true
	}
	if len(documented) < 40 {
		t.Fatalf("found only %d files in DESIGN.md's UI file table; the parsing is broken", len(documented))
	}

	paths, err := filepath.Glob(filepath.Join("internal", "ui", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]bool{}
	for _, p := range paths {
		if name := filepath.Base(p); !strings.HasSuffix(name, "_test.go") {
			actual[name] = true
		}
	}

	for _, name := range sortedKeys(actual) {
		if !documented[name] {
			t.Errorf("DESIGN.md's UI file table doesn't list internal/ui/%s", name)
		}
	}
	for _, name := range sortedKeys(documented) {
		if !actual[name] {
			t.Errorf("DESIGN.md's UI file table lists %s, which isn't in internal/ui", name)
		}
	}
}

// uiFileTable returns the table rows under "### Files" in DESIGN.md's
// "## 5. The UI package" section: the run of lines starting with "|".
func uiFileTable(t *testing.T, design string) string {
	t.Helper()
	_, section, ok := strings.Cut(design, "\n## 5. The UI package\n")
	if !ok {
		t.Fatal(`DESIGN.md has no "## 5. The UI package" section`)
	}
	_, files, ok := strings.Cut(section, "\n### Files\n")
	if !ok {
		t.Fatal(`DESIGN.md's UI package section has no "### Files" heading`)
	}
	var rows []string
	started := false
	for line := range strings.SplitSeq(files, "\n") {
		if strings.HasPrefix(line, "|") {
			started = true
			rows = append(rows, line)
		} else if started {
			break
		}
	}
	return strings.Join(rows, "\n")
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
