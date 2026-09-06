package org

import "strings"

// RenderHeadline serializes h and its entire subtree back into org-mode
// text. It's the inverse of Parse for a single headline: the result is
// meant to be re-parsed (e.g. after a round trip through an external
// editor), not to reproduce the original file byte-for-byte.
func RenderHeadline(h *Headline) string {
	var b strings.Builder
	writeHeadline(&b, h)
	return b.String()
}

// RenderFile serializes an entire file (preamble plus every top-level
// headline and its subtree) back into org-mode text. Like RenderHeadline,
// this isn't a byte-for-byte round trip of the original file: headline
// lines, planning lines, and property drawers are regenerated from their
// parsed fields, so they may come out cosmetically different even when
// unchanged (spacing, drawer key order within a line, and so on). Body
// text, unrecognized content, and the file preamble are stored as raw
// lines and do round-trip exactly.
func RenderFile(f *File) string {
	var b strings.Builder
	for _, line := range f.Preamble {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, h := range f.Headlines {
		writeHeadline(&b, h)
	}
	return b.String()
}

func writeHeadline(b *strings.Builder, h *Headline) {
	b.WriteString(strings.Repeat("*", h.Level))
	b.WriteByte(' ')
	if h.Keyword != "" {
		b.WriteString(h.Keyword)
		b.WriteByte(' ')
	}
	if h.Priority != "" {
		b.WriteString("[#" + h.Priority + "] ")
	}
	b.WriteString(h.Title)
	if len(h.Tags) > 0 {
		b.WriteString("   :" + strings.Join(h.Tags, ":") + ":")
	}
	b.WriteByte('\n')

	indent := strings.Repeat(" ", h.Level+1)

	if planning := planningLine(h); planning != "" {
		b.WriteString(indent + planning + "\n")
	}

	if len(h.PropertyOrder) > 0 {
		b.WriteString(indent + ":PROPERTIES:\n")
		for _, k := range h.PropertyOrder {
			b.WriteString(indent + ":" + k + ": " + h.Properties[k] + "\n")
		}
		b.WriteString(indent + ":END:\n")
	}

	for _, line := range h.Body {
		b.WriteString(line + "\n")
	}

	for _, c := range h.Children {
		writeHeadline(b, c)
	}
}

func planningLine(h *Headline) string {
	var parts []string
	if h.Scheduled != nil {
		parts = append(parts, "SCHEDULED: "+h.Scheduled.String())
	}
	if h.Deadline != nil {
		parts = append(parts, "DEADLINE: "+h.Deadline.String())
	}
	if h.Closed != nil {
		parts = append(parts, "CLOSED: "+h.Closed.String())
	}
	return strings.Join(parts, " ")
}
