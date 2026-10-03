package org

import "testing"

// TestParseLinksMatchesDescriptionContainingBrackets guards a real bug: a
// description with a literal bracket in it (e.g. a formatter's output for
// a page titled "Bracket [disambiguation]") is perfectly valid org-mode
// link syntax — org-mode itself just looks for the nearest following
// "]]", it doesn't forbid brackets in the description. The link regexp
// used to exclude them outright, so a link like this never matched at
// all, leaving its url looking "bare" on every subsequent scan and
// sending it through the formatter again.
func TestParseLinksMatchesDescriptionContainingBrackets(t *testing.T) {
	links := ParseLinks("See [[https://example.com][Bracket [disambiguation] page]] for context")
	if len(links) != 1 {
		t.Fatalf("links = %#v, want exactly 1", links)
	}
	if links[0].URL != "https://example.com" {
		t.Errorf("URL = %q, want https://example.com", links[0].URL)
	}
	if links[0].Description != "Bracket [disambiguation] page" {
		t.Errorf("Description = %q, want %q", links[0].Description, "Bracket [disambiguation] page")
	}
}

// TestParseLinksStopsAtNearestClosingBracketsAcrossMultipleLinks guards
// against the opposite failure mode: the permissive, non-greedy
// description shouldn't swallow past its own link's "]]" into whatever
// follows, even when a second link comes right after.
func TestParseLinksStopsAtNearestClosingBracketsAcrossMultipleLinks(t *testing.T) {
	links := ParseLinks("[[https://a.example.com][A [bracketed] note]] and [[https://b.example.com][B]]")
	if len(links) != 2 {
		t.Fatalf("links = %#v, want exactly 2", links)
	}
	if links[0].Description != "A [bracketed] note" {
		t.Errorf("first description = %q, want %q", links[0].Description, "A [bracketed] note")
	}
	if links[1].URL != "https://b.example.com" || links[1].Description != "B" {
		t.Errorf("second link = %#v, want https://b.example.com / B", links[1])
	}
}

func TestParseLinksBareLinkHasNoDescription(t *testing.T) {
	links := ParseLinks("x [[https://a.example.com]] y")
	if len(links) != 1 || links[0].URL != "https://a.example.com" || links[0].Description != "" {
		t.Errorf("links = %#v, want one link with no description", links)
	}
}

func TestParseLinksNilWhenNone(t *testing.T) {
	if got := ParseLinks("plain text"); got != nil {
		t.Errorf("ParseLinks = %#v, want nil", got)
	}
}

func TestLinkIndexesReportOffsets(t *testing.T) {
	text := "a [[u][d]] b [[v]]"
	idx := LinkIndexes(text)
	if len(idx) != 2 {
		t.Fatalf("indexes = %v, want 2 links", idx)
	}
	if got := text[idx[0][0]:idx[0][1]]; got != "[[u][d]]" {
		t.Errorf("first link span = %q, want [[u][d]]", got)
	}
	if got := text[idx[0][4]:idx[0][5]]; got != "d" {
		t.Errorf("first description = %q, want d", got)
	}
	if idx[1][4] != -1 {
		t.Errorf("second link description offset = %d, want -1 (none)", idx[1][4])
	}
}

func TestFormatLinksRoundTripsParseLinks(t *testing.T) {
	in := "[[https://a.example.com][A]] [[https://b.example.com][B]]"
	if got := FormatLinks(ParseLinks(in)); got != in {
		t.Errorf("FormatLinks(ParseLinks(%q)) = %q", in, got)
	}
}
