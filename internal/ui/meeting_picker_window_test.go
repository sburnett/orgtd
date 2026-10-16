package ui

import "testing"

func TestWindowPickerLines(t *testing.T) {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	id := func(s string) string { return s }
	for idx := 0; idx < 50; idx++ {
		out := windowPickerLines(lines, idx, 20, id)
		if len(out) > 20 {
			t.Fatalf("idx %d: %d rows", idx, len(out))
		}
		found := false
		for _, l := range out {
			if l == lines[idx] {
				found = true
			}
		}
		if !found {
			t.Fatalf("idx %d not visible: %v", idx, out)
		}
	}
}
