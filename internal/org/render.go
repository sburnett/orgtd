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
// headline and its subtree) back into org-mode text. The preamble, body
// text and every headline nothing has changed since it was parsed come
// out byte-for-byte as they were read (see Headline.src). A changed
// headline's own headline, planning and drawer lines are regenerated
// from its fields, as in RenderHeadline, so they may come out
// cosmetically different and lose what the parser doesn't model.
func RenderFile(f *File) string {
	s, _ := renderFile(f, nil)
	return s
}

// HeadlineLines returns the 1-based line number of every headline in f
// as the file stands on disk: from rendering f, when that's what's on
// disk (orgtd wrote it, or it round-trips exactly), or else from each
// headline's SrcStart — the file hasn't been written since it was
// parsed, since a write would have made disk and rendering agree. A New
// file never written uses the rendering too.
func HeadlineLines(f *File) map[*Headline]int {
	lines := map[*Headline]int{}
	content, _ := renderFile(f, lines)
	if f.Base == nil || content == string(f.Base) {
		return lines
	}
	for h := range lines {
		lines[h] = h.SrcStart + 1
	}
	return lines
}

// renderFile is RenderFile, also recording in lines (if non-nil) each
// headline's 1-based line number in the result.
func renderFile(f *File, lines map[*Headline]int) (string, map[*Headline]int) {
	var b strings.Builder
	for _, line := range f.Preamble {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	n := len(f.Preamble)
	var walk func(h *Headline)
	walk = func(h *Headline) {
		if lines != nil {
			lines[h] = n + 1
		}
		before := b.Len()
		writeOwnText(&b, h)
		n += strings.Count(b.String()[before:], "\n")
		for _, c := range h.Children {
			walk(c)
		}
	}
	for _, h := range f.Headlines {
		walk(h)
	}
	return b.String(), lines
}

// RenderEntry serializes h's own headline line, planning line, property
// drawer, and body text — but not its children — back into org-mode
// text. Used to build the buffer an existing entry is edited in: unlike
// RenderHeadline/RenderFile, the result on its own is not meant to be
// re-parsed as-is (a real headline needs a parent scope, i.e. nothing
// deeper than it, to close its subtree), only after the caller re-adds
// whatever context it stripped away.
func RenderEntry(h *Headline) string {
	var b strings.Builder
	writeHeadlineFields(&b, h)
	return b.String()
}

// writeOwnText writes h's own text as it was parsed, if nothing has
// changed it since (see Headline.src), or else regenerated from its
// fields.
func writeOwnText(b *strings.Builder, h *Headline) {
	var r strings.Builder
	writeHeadlineFields(&r, h)
	if text := r.String(); h.src == "" || text != h.srcFields {
		b.WriteString(text)
		return
	}
	b.WriteString(h.src)
}

func writeHeadline(b *strings.Builder, h *Headline) {
	writeHeadlineFields(b, h)
	for _, c := range h.Children {
		writeHeadline(b, c)
	}
}

func writeHeadlineFields(b *strings.Builder, h *Headline) {
	b.WriteString(strings.Repeat("*", h.Level))
	b.WriteByte(' ')
	if h.Keyword != "" {
		b.WriteString(h.Keyword)
		b.WriteByte(' ')
	}
	if h.Priority != "" {
		b.WriteString("[#" + h.Priority + "]")
		// A title that is itself tag-shaped (as in "[#A]:x:") goes
		// straight after the cookie, as it was parsed: after a space it
		// would parse as tags.
		if !tagsRe.MatchString(" " + h.Title) {
			b.WriteByte(' ')
		}
	}
	b.WriteString(h.Title)
	if len(h.Tags) > 0 {
		b.WriteString("   :" + strings.Join(h.Tags, ":") + ":")
	}
	b.WriteByte('\n')

	indent := strings.Repeat(" ", h.Level+1)

	planning := PlanningLine(h)
	if planning != "" {
		b.WriteString(indent + planning + "\n")
	}

	// A body whose first line looks like planning keeps an empty drawer
	// in front of it (as it must have had, to parse as body), or it
	// would come back as the entry's planning. Otherwise an empty drawer
	// is left out, so removing the last property removes the drawer.
	needDrawer := planning == "" && len(h.Body) > 0 && applyPlanningLine(&Headline{}, h.Body[0])
	if len(h.PropertyOrder) > 0 || needDrawer {
		b.WriteString(indent + ":PROPERTIES:\n")
		for _, k := range h.PropertyOrder {
			b.WriteString(indent + ":" + k + ": " + h.Properties[k] + "\n")
		}
		b.WriteString(indent + ":END:\n")
	}

	for _, line := range h.Body {
		b.WriteString(line + "\n")
	}
}

// PlanningLine is h's planning line without its indentation, e.g.
// "SCHEDULED: <2026-11-14 Sat> CLOSED: [2026-11-14 Sat 09:30]", or "" if
// h has no planning.
func PlanningLine(h *Headline) string {
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

// DedentEntry removes one level's worth of indentation — level+1
// characters, the fixed width writeHeadlineFields always uses, both for
// the bullet ("<stars> ") on the first line and for the span of spaces
// indenting every other line (planning, properties, body) so they align
// under it — from every line of s (see RenderEntry). Used to build the
// buffer an entry is edited or inserted in: with the bullet gone, leaving
// the rest of the entry indented under where it used to be would look
// disjointed, so the whole entry is dedented together. Blank lines are
// left alone. IndentEntry reverses this once the edit comes back.
func DedentEntry(s string, level int) string {
	width := level + 1
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i == 0 {
			// The bullet is "<stars> ", not spaces, but is always exactly
			// width bytes wide regardless of what follows.
			if len(line) < width {
				continue
			}
			lines[i] = line[width:]
			continue
		}
		n := 0
		for n < width && n < len(line) && line[n] == ' ' {
			n++
		}
		lines[i] = line[n:]
	}
	return strings.Join(lines, "\n")
}

// IndentEntry reverses DedentEntry once the edit comes back: restores the
// bullet onto s's first line, and the matching span of indentation onto
// every other non-blank line, so the whole entry re-parses as a real
// headline at its original level with its planning/properties/body lines
// indented the way writeHeadlineFields expects. Blank lines are left
// alone, matching how DedentEntry treats them.
func IndentEntry(s string, level int) string {
	indent := strings.Repeat(" ", level+1)
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = strings.Repeat("*", level) + " " + line
			continue
		}
		if line == "" {
			continue
		}
		lines[i] = indent + line
	}
	return strings.Join(lines, "\n")
}
