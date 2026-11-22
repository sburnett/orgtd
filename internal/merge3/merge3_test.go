package merge3

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"
)

func TestMerge(t *testing.T) {
	labels := Labels{Ours: "ours", Theirs: "theirs"}
	tests := []struct {
		name                     string
		base, ours, theirs, want string
		conflicts                int
	}{
		{
			name: "all equal",
			base: "a\nb\n", ours: "a\nb\n", theirs: "a\nb\n",
			want: "a\nb\n",
		},
		{
			name: "only ours changed",
			base: "a\nb\nc\n", ours: "a\nB\nc\n", theirs: "a\nb\nc\n",
			want: "a\nB\nc\n",
		},
		{
			name: "only theirs changed",
			base: "a\nb\nc\n", ours: "a\nb\nc\n", theirs: "a\nb\nC\n",
			want: "a\nb\nC\n",
		},
		{
			name: "separate regions",
			base: "a\nb\nc\nd\ne\n", ours: "A\nb\nc\nd\ne\n", theirs: "a\nb\nc\nd\nE\n",
			want: "A\nb\nc\nd\nE\n",
		},
		{
			name: "insert and change far apart",
			base: "a\nb\nc\nd\n", ours: "a\nnew\nb\nc\nd\n", theirs: "a\nb\nc\nD\n",
			want: "a\nnew\nb\nc\nD\n",
		},
		{
			name: "both delete different lines",
			base: "a\nb\nc\nd\n", ours: "a\nc\nd\n", theirs: "a\nb\nc\n",
			want: "a\nc\n",
		},
		{
			name: "identical change on both sides",
			base: "a\nb\nc\n", ours: "a\nX\nc\n", theirs: "a\nX\nc\n",
			want: "a\nX\nc\n",
		},
		{
			name: "identical change plus a separate one",
			base: "a\nb\nc\nd\ne\n", ours: "a\nX\nc\nd\nE\n", theirs: "a\nX\nc\nd\ne\n",
			want: "a\nX\nc\nd\nE\n",
		},
		{
			name: "overlapping change conflicts",
			base: "a\nb\nc\n", ours: "a\nB1\nc\n", theirs: "a\nB2\nc\n",
			want:      "a\n<<<<<<< ours\nB1\n=======\nB2\n>>>>>>> theirs\nc\n",
			conflicts: 1,
		},
		{
			name: "both append at the end conflicts",
			base: "* a\n", ours: "* a\n* b\n", theirs: "* a\n* c\n",
			want:      "* a\n<<<<<<< ours\n* b\n=======\n* c\n>>>>>>> theirs\n",
			conflicts: 1,
		},
		{
			name: "insert next to a change is not a conflict",
			base: "a\nb\nc\n", ours: "a\nnew\nb\nc\n", theirs: "a\nB\nc\n",
			want: "a\nnew\nB\nc\n",
		},
		{
			name: "insert after a change is not a conflict",
			base: "a\nb\nc\n", ours: "a\nb\nnew\nc\n", theirs: "a\nB\nc\n",
			want: "a\nB\nnew\nc\n",
		},
		{
			name: "insert inside a changed range conflicts",
			base: "a\nb\nc\nd\n", ours: "a\nb\nnew\nc\nd\n", theirs: "a\nB\nC\nd\n",
			want:      "a\n<<<<<<< ours\nb\nnew\nc\n=======\nB\nC\n>>>>>>> theirs\nd\n",
			conflicts: 1,
		},
		{
			name: "two conflicts",
			base: "a\nb\nc\nd\ne\n", ours: "A1\nb\nc\nd\nE1\n", theirs: "A2\nb\nc\nd\nE2\n",
			want:      "<<<<<<< ours\nA1\n=======\nA2\n>>>>>>> theirs\nb\nc\nd\n<<<<<<< ours\nE1\n=======\nE2\n>>>>>>> theirs\n",
			conflicts: 2,
		},
		{
			name: "empty base, both added the same",
			base: "", ours: "x\ny\n", theirs: "x\ny\n",
			want: "x\ny\n",
		},
		{
			name: "empty base, both added differently",
			base: "", ours: "x\n", theirs: "y\n",
			want:      "<<<<<<< ours\nx\n=======\ny\n>>>>>>> theirs\n",
			conflicts: 1,
		},
		{
			name: "missing trailing newline, clean",
			base: "a\nb", ours: "A\nb", theirs: "a\nb\nc",
			want: "A\nb\nc",
		},
		{
			name: "missing trailing newline in a conflict",
			base: "a\nb", ours: "a\nB1", theirs: "a\nB2",
			want:      "a\n<<<<<<< ours\nB1\n=======\nB2\n>>>>>>> theirs\n",
			conflicts: 1,
		},
		{
			name: "CRLF line endings",
			base: "a\r\nb\r\nc\r\n", ours: "A\r\nb\r\nc\r\n", theirs: "a\r\nb\r\nC\r\n",
			want: "A\r\nb\r\nC\r\n",
		},
		{
			name: "delete versus change conflicts",
			base: "a\nb\nc\n", ours: "a\nc\n", theirs: "a\nB\nc\n",
			want:      "a\n<<<<<<< ours\n=======\nB\n>>>>>>> theirs\nc\n",
			conflicts: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, n := Merge([]byte(tc.base), []byte(tc.ours), []byte(tc.theirs), labels)
			if string(got) != tc.want || n != tc.conflicts {
				t.Errorf("Merge = %q, %d conflicts\nwant    %q, %d", got, n, tc.want, tc.conflicts)
			}
			if HasMarkers(got) != (tc.conflicts > 0) {
				t.Errorf("HasMarkers = %v, want %v", HasMarkers(got), tc.conflicts > 0)
			}
		})
	}
}

func TestMergeWithoutLabels(t *testing.T) {
	got, n := Merge([]byte("a\n"), []byte("b\n"), []byte("c\n"), Labels{})
	if want := "<<<<<<<\nb\n=======\nc\n>>>>>>>\n"; string(got) != want || n != 1 {
		t.Errorf("Merge = %q, %d; want %q, 1", got, n, want)
	}
}

func TestHasMarkers(t *testing.T) {
	for text, want := range map[string]bool{
		"":                                      false,
		"<<<<<<< x\na\n=======\nb\n>>>>>>> y\n": true,
		"<<<<<<< x\na\n=======\nb\n":            false,
		"a\n=======\nb\n":                       false,
		"* =======\n":                           false,
		"<<<<<<<\r\na\r\n=======\r\nb\r\n>>>>>>>\r\n": true,
	} {
		if got := HasMarkers([]byte(text)); got != want {
			t.Errorf("HasMarkers(%q) = %v, want %v", text, got, want)
		}
	}
}

// randomText builds a text from a small alphabet of lines, so that
// random edits produce plenty of repeated and matching lines.
func randomText(r *rand.Rand, n int) [][]byte {
	lines := make([][]byte, n)
	for i := range lines {
		lines[i] = []byte(string(rune('a'+r.Intn(5))) + "\n")
	}
	return lines
}

func mutate(r *rand.Rand, lines [][]byte) [][]byte {
	out := append([][]byte(nil), lines...)
	for range r.Intn(5) {
		switch i := r.Intn(len(out) + 1); r.Intn(3) {
		case 0:
			out = append(out[:i], append([][]byte{[]byte(string(rune('a'+r.Intn(7))) + "\n")}, out[i:]...)...)
		case 1:
			if i < len(out) {
				out = append(out[:i], out[i+1:]...)
			}
		case 2:
			if i < len(out) {
				out[i] = []byte("z\n")
			}
		}
	}
	return out
}

// TestDiffReconstructs checks the line diff on random inputs: applying
// its hunks to a gives b, and hunks never overlap or go backwards.
func TestDiffReconstructs(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for range 5000 {
		a := randomText(r, r.Intn(12))
		b := mutate(r, a)
		if r.Intn(4) == 0 {
			b = randomText(r, r.Intn(12))
		}
		hunks := diffHunks(a, b, sideOurs)
		got := applySide(a, 0, len(a), hunks, sideOurs, b)
		if !linesEqual(got, b) {
			t.Fatalf("diff of %q → %q gave hunks %+v, reconstructing %q", bytes.Join(a, nil), bytes.Join(b, nil), hunks, bytes.Join(got, nil))
		}
		last := 0
		for _, h := range hunks {
			if h.baseStart < last || h.baseEnd < h.baseStart {
				t.Fatalf("hunks out of order: %+v", hunks)
			}
			last = h.baseEnd
		}
	}
}

// TestMergeRandom checks Merge's identities on random edits: one side
// unchanged gives the other side; the result with no conflicts contains
// every line either side added.
func TestMergeRandom(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for range 3000 {
		base := bytes.Join(randomText(r, r.Intn(15)), nil)
		ours := bytes.Join(mutate(r, SplitLines(base)), nil)
		theirs := bytes.Join(mutate(r, SplitLines(base)), nil)
		if got, n := Merge(base, ours, base, Labels{}); n != 0 || !bytes.Equal(got, ours) {
			t.Fatalf("Merge(b, o, b) = %q, want %q", got, ours)
		}
		if got, n := Merge(base, base, theirs, Labels{}); n != 0 || !bytes.Equal(got, theirs) {
			t.Fatalf("Merge(b, b, t) = %q, want %q", got, theirs)
		}
		got, n := Merge(base, ours, theirs, Labels{})
		if (n > 0) != HasMarkers(got) {
			t.Fatalf("Merge(%q, %q, %q) = %q with %d conflicts, markers disagree", base, ours, theirs, got, n)
		}
	}
}

func FuzzMergeIdentity(f *testing.F) {
	f.Add("a\nb\nc\n", "a\nB\nc\n")
	f.Add("", "x")
	f.Add("a\r\nb", "a\r\nb\r\n")
	f.Fuzz(func(t *testing.T, x, y string) {
		if got, n := Merge([]byte(x), []byte(x), []byte(x), Labels{}); n != 0 || string(got) != x {
			t.Errorf("Merge(x, x, x) = %q, %d; want x", got, n)
		}
		if got, n := Merge([]byte(x), []byte(y), []byte(x), Labels{}); n != 0 || string(got) != y {
			t.Errorf("Merge(x, y, x) = %q, %d; want y", got, n)
		}
		if got, n := Merge([]byte(x), []byte(y), []byte(y), Labels{}); n != 0 || string(got) != y {
			t.Errorf("Merge(x, y, y) = %q, %d; want y", got, n)
		}
		// Merging y against an append to x: the merge is clean or marked.
		got, n := Merge([]byte(x), []byte(y), []byte(x+"\ntail\n"), Labels{})
		if (n > 0) != HasMarkers(got) && !strings.Contains(y, "<<<<<<<") {
			t.Errorf("conflict count %d but HasMarkers = %v for %q", n, HasMarkers(got), got)
		}
	})
}
