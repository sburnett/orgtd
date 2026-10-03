package org

import (
	"regexp"
	"strings"
)

// Link is one org-mode "[[url][description]]" (or bare "[[url]]") link.
// Description is empty for the no-description form.
type Link struct {
	URL, Description string
}

// linkRe matches an existing org-mode link, "[[url]]" or
// "[[url][description]]" — group 1 is the url, group 2 the description
// (absent for the no-description form).
//
// The description is matched non-greedily against *any* character, up
// to the nearest following "]]" — deliberately not excluding "[" and
// "]" the way the url group does, matching real org-mode's own lenient
// link grammar (it finds the closest "]]", rather than forbidding
// brackets in a description outright). Without this, a formatter output
// like "[[https://example.com][Some [bracketed] title]]" — a
// perfectly valid org-mode link — would fail to match at all: the url
// inside it would then still look "bare" on the next :format-links or
// in-editor pass, sending it through the formatter again and
// double-wrapping it.
var linkRe = regexp.MustCompile(`\[\[([^\]\[]+)\](?:\[(.*?)\])?\]`)

// ParseLinks extracts every org-mode link in text, in order. nil if there
// are none.
func ParseLinks(text string) []Link {
	matches := linkRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	links := make([]Link, len(matches))
	for i, m := range matches {
		links[i] = Link{URL: m[1], Description: m[2]}
	}
	return links
}

// LinkIndexes returns the byte offsets of every org-mode link in text, as
// regexp.FindAllStringSubmatchIndex would: for each link, [start, end,
// urlStart, urlEnd, descStart, descEnd], with descStart/descEnd -1 for a
// link with no description. For callers that need to know *where* the
// links are (to render around them, or to avoid them) rather than just
// what they say.
func LinkIndexes(text string) [][]int {
	return linkRe.FindAllStringSubmatchIndex(text, -1)
}

// FormatLinks is the inverse of ParseLinks: renders links back into a
// single space-separated "[[url][description]]" string, the shape of a
// property value like GCAL_EVENT_LINKS.
func FormatLinks(links []Link) string {
	parts := make([]string, len(links))
	for i, l := range links {
		parts[i] = "[[" + l.URL + "][" + l.Description + "]]"
	}
	return strings.Join(parts, " ")
}
