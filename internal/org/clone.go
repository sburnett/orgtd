package org

// CloneHeadline returns a deep copy of h and its entire subtree,
// independent of the original (mutating one never affects the other).
// Parent is left nil on the returned root; children's Parent pointers
// are set to their (also cloned) parent.
func CloneHeadline(h *Headline) *Headline {
	clone := &Headline{
		Level:     h.Level,
		Keyword:   h.Keyword,
		Priority:  h.Priority,
		Title:     h.Title,
		Scheduled: h.Scheduled,
		Deadline:  h.Deadline,
		Closed:    h.Closed,
	}
	if h.Tags != nil {
		clone.Tags = append([]string(nil), h.Tags...)
	}
	if h.Body != nil {
		clone.Body = append([]string(nil), h.Body...)
	}
	if h.PropertyOrder != nil {
		clone.PropertyOrder = append([]string(nil), h.PropertyOrder...)
	}
	if h.Properties != nil {
		clone.Properties = make(map[string]string, len(h.Properties))
		for k, v := range h.Properties {
			clone.Properties[k] = v
		}
	}
	for _, c := range h.Children {
		cc := CloneHeadline(c)
		cc.Parent = clone
		clone.Children = append(clone.Children, cc)
	}
	return clone
}
