package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sburnett/orgtd/internal/org"
)

// runContext is the JSON document a context-enabled :run command finds
// at $ORGTD_CONTEXT (see README's "Passing context to a command").
type runContext struct {
	Dir     string            `json:"dir"`
	View    string            `json:"view"`
	File    string            `json:"file,omitempty"`
	Entries []runContextEntry `json:"entries"`
}

// runContextEntry is one entry in runContext.Entries.
type runContextEntry struct {
	File        string             `json:"file"`
	Line        int                `json:"line"`
	Level       int                `json:"level"`
	Todo        string             `json:"todo"`
	Priority    string             `json:"priority"`
	Title       string             `json:"title"`
	Tags        []string           `json:"tags"`
	Scheduled   string             `json:"scheduled"`
	Deadline    string             `json:"deadline"`
	Closed      string             `json:"closed"`
	Properties  map[string]string  `json:"properties"`
	Body        string             `json:"body"`
	OutlinePath []string           `json:"outline_path"`
	Ancestors   []runContextParent `json:"ancestors"`
	Raw         string             `json:"raw"`
}

// runContextParent is one of an entry's ancestors, outermost first.
type runContextParent struct {
	Line  int    `json:"line"`
	Level int    `json:"level"`
	Todo  string `json:"todo"`
	Title string `json:"title"`
}

// runTargets is the entries a :run command gets as context: the visual
// selection ":" was typed from (m.commandSelection), or else the entry
// under the cursor, or none (a file's header row, an empty view).
func (m *Model) runTargets() []*org.Headline {
	if m.commandSelection != nil {
		return m.commandSelection
	}
	if h := m.currentHeadline(); h != nil {
		return []*org.Headline{h}
	}
	return nil
}

// buildRunContext gathers the context for targets. Call it only after
// every file is saved, so line numbers match what's on disk.
func (m *Model) buildRunContext(targets []*org.Headline) runContext {
	ctx := runContext{Dir: m.ws.Dir, View: viewSpecs[m.view].command, Entries: []runContextEntry{}}
	if len(targets) == 0 {
		if f := m.currentRowFile(); f != nil {
			ctx.File = f.Path
		}
	}
	lineCache := map[*org.File]map[*org.Headline]int{}
	lineOf := func(f *org.File, h *org.Headline) int {
		if f == nil {
			return 0
		}
		if lineCache[f] == nil {
			lineCache[f] = org.HeadlineLines(f)
		}
		return lineCache[f][h]
	}
	for _, h := range targets {
		f := m.ws.FileOf(h)
		e := runContextEntry{
			Level:      h.Level,
			Todo:       h.Keyword,
			Priority:   h.Priority,
			Title:      h.Title,
			Tags:       append([]string{}, h.Tags...),
			Scheduled:  h.Scheduled.String(),
			Deadline:   h.Deadline.String(),
			Closed:     h.Closed.String(),
			Properties: map[string]string{},
			Body:       strings.Join(h.TrimmedBody(), "\n"),
			Raw:        org.RenderEntry(h),
			Line:       lineOf(f, h),
		}
		if f != nil {
			e.File = f.Path
			e.OutlinePath = []string{filepath.Base(f.Path)}
		}
		for k, v := range h.Properties {
			e.Properties[k] = v
		}
		var ancestors []*org.Headline
		for p := h.Parent; p != nil; p = p.Parent {
			ancestors = append([]*org.Headline{p}, ancestors...)
		}
		e.Ancestors = []runContextParent{}
		for _, p := range ancestors {
			e.Ancestors = append(e.Ancestors, runContextParent{Line: lineOf(f, p), Level: p.Level, Todo: p.Keyword, Title: p.Title})
			e.OutlinePath = append(e.OutlinePath, p.Title)
		}
		e.OutlinePath = append(e.OutlinePath, h.Title)
		ctx.Entries = append(ctx.Entries, e)
	}
	return ctx
}

// env returns ctx as ORGTD_* environment variables (without
// ORGTD_CONTEXT, which names the file ctx is written to). The
// per-entry ones are set only when there's exactly one entry.
func (ctx runContext) env() []string {
	env := []string{
		"ORGTD_DIR=" + ctx.Dir,
		"ORGTD_VIEW=" + ctx.View,
		"ORGTD_COUNT=" + strconv.Itoa(len(ctx.Entries)),
	}
	if ctx.File != "" {
		env = append(env, "ORGTD_FILE="+ctx.File)
	}
	if len(ctx.Entries) != 1 {
		return env
	}
	e := ctx.Entries[0]
	tags := ""
	if len(e.Tags) > 0 {
		tags = ":" + strings.Join(e.Tags, ":") + ":"
	}
	return append(env,
		"ORGTD_FILE="+e.File,
		"ORGTD_LINE="+strconv.Itoa(e.Line),
		"ORGTD_TITLE="+e.Title,
		"ORGTD_TODO="+e.Todo,
		"ORGTD_TAGS="+tags,
		"ORGTD_OUTLINE_PATH="+strings.Join(e.OutlinePath, "/"),
		"ORGTD_ID="+e.Properties["ID"],
	)
}

// writeRunContext writes ctx as JSON to a new file in the system temp
// directory — readable only by you (os.CreateTemp's 0600), and never
// inside the org directory, where a VCS tool being run would see it as
// an untracked file — and returns its path. The caller removes it.
func writeRunContext(ctx runContext) (string, error) {
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding context: %w", err)
	}
	f, err := os.CreateTemp("", "orgtd-context-*.json")
	if err != nil {
		return "", fmt.Errorf("creating context file: %w", err)
	}
	_, werr := f.Write(append(data, '\n'))
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("writing context file: %w", errors.Join(werr, cerr))
	}
	return f.Name(), nil
}
