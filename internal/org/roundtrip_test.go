package org

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/roundtrip/*.org.regen from the current renderer")

// The golden round-trip corpus, testdata/roundtrip, holds one .org file
// per group of constructs: the org syntax orgtd models, the syntax it
// doesn't (ranges, LOGBOOK, late drawers, ...), and line-ending edge
// cases. Each file is checked two ways:
//
//   - Unedited, RenderFile must give the file back byte for byte, up to
//     wantVerbatim's normalization.
//   - Regenerated, as if every entry had been edited, RenderFile's output
//     must match the checked-in <name>.org.regen (rewrite them with
//     go test ./internal/org -run TestGoldenRoundTrip -update, and review
//     the diff), and must parse back to the same fields as the original.
//
// The .org files are also FuzzParseRender's seed corpus.
func TestGoldenRoundTrip(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "roundtrip", "*.org"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no files in testdata/roundtrip")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := ParseContent(data, path)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := RenderFile(f), wantVerbatim(string(data)); got != want {
				t.Errorf("unedited RenderFile changed the file\n%s", lineDiff(want, got))
			}

			fields := dumpFile(f)
			regen := regenerate(f)
			golden := path + ".regen"
			if *update {
				if err := os.WriteFile(golden, []byte(regen), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if regen != string(want) {
				t.Errorf("regenerated RenderFile differs from %s\n%s", golden, lineDiff(string(want), regen))
			}

			re, err := Parse(strings.NewReader(regen), path)
			if err != nil {
				t.Fatal(err)
			}
			if got := dumpFile(re); got != fields {
				t.Errorf("regenerating changed the parsed fields\n%s", lineDiff(fields, got))
			}
		})
	}
}

// FuzzParseRender checks, for any input, the two round-trip properties
// TestGoldenRoundTrip checks for its corpus: an unedited file renders
// back verbatim, and regenerating every entry keeps every parsed field.
func FuzzParseRender(f *testing.F) {
	paths, _ := filepath.Glob(filepath.Join("testdata", "roundtrip", "*.org"))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(data))
	}
	f.Add("")
	f.Add("\n")
	f.Add("*")
	f.Add("* a\r")
	f.Fuzz(func(t *testing.T, x string) {
		file, err := ParseContent([]byte(x), "fuzz.org")
		if err != nil {
			t.Skip() // a line over the scanner's 1MB limit
		}
		if got, want := RenderFile(file), wantVerbatim(x); got != want {
			t.Fatalf("unedited RenderFile changed %q\n%s", x, lineDiff(want, got))
		}
		if wantVerbatim(wantVerbatim(x)) != wantVerbatim(x) {
			// A line ending in "\r\r": RenderFile writes it back ending
			// in "\r", which the next Parse drops as half of a CRLF.
			t.Skip()
		}
		fields := dumpFile(file)
		regen := regenerate(file)
		re, err := Parse(strings.NewReader(regen), "fuzz.org")
		if err != nil {
			t.Skip()
		}
		if got := dumpFile(re); got != fields {
			t.Fatalf("regenerating %q as %q changed the parsed fields\n%s", x, regen, lineDiff(fields, got))
		}
	})
}

// wantVerbatim is what RenderFile gives back for an unedited file with
// content x. Parse reads lines as bufio.ScanLines does, and RenderFile
// ends every line with "\n", so two things change: a "\r" ending a line
// is dropped (a CRLF file comes back LF), and a missing final newline is
// added. Nothing else may.
func wantVerbatim(x string) string {
	var b strings.Builder
	for x != "" {
		line, rest, _ := strings.Cut(x, "\n")
		b.WriteString(strings.TrimSuffix(line, "\r"))
		b.WriteByte('\n')
		x = rest
	}
	return b.String()
}

// regenerate renders f as if every entry in it had been edited: from its
// parsed fields, not its source text. It clears that source text, so
// call it last.
func regenerate(f *File) string {
	Walk(f.Headlines, func(h *Headline) { h.src = "" })
	return RenderFile(f)
}

// dumpFile describes every field Parse fills in, one per line, for
// comparing two parses. It leaves out SrcStart/SrcEnd and the source
// text, which legitimately differ between a file and its regeneration,
// and Properties' nil-versus-empty, which nothing renders.
func dumpFile(f *File) string {
	var b strings.Builder
	for _, line := range f.Preamble {
		fmt.Fprintf(&b, "preamble %q\n", line)
	}
	Walk(f.Headlines, func(h *Headline) {
		fmt.Fprintf(&b, "headline level=%d keyword=%q priority=%q title=%q tags=%q children=%d\n",
			h.Level, h.Keyword, h.Priority, h.Title, h.Tags, len(h.Children))
		for _, p := range []struct {
			name string
			ts   *Timestamp
		}{{"scheduled", h.Scheduled}, {"deadline", h.Deadline}, {"closed", h.Closed}} {
			if p.ts != nil {
				fmt.Fprintf(&b, "  %s %q\n", p.name, p.ts.String())
			}
		}
		for _, k := range h.PropertyOrder {
			fmt.Fprintf(&b, "  property %q = %q\n", k, h.Properties[k])
		}
		for _, line := range h.Body {
			fmt.Fprintf(&b, "  body %q\n", line)
		}
	})
	return b.String()
}

// lineDiff shows want and got side by side from their first differing
// line, enough to see what changed without a diff library.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(w) && i < len(g) && w[i] == g[i] {
		i++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "first difference at line %d:\n", i+1)
	for j := i; j < i+5 && (j < len(w) || j < len(g)); j++ {
		var wl, gl string
		if j < len(w) {
			wl = w[j]
		}
		if j < len(g) {
			gl = g[j]
		}
		fmt.Fprintf(&b, "  want %q\n  got  %q\n", wl, gl)
	}
	return b.String()
}
