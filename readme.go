// Package orgtd exists only to embed README.md into the binary — go:embed
// can't reach outside the directory of the file containing the
// directive, and README.md lives at the module root, so this is the
// only place that can hold it. See cmd/orgtd/main.go, which passes
// Readme to the UI so :help (internal/ui) can show it without bundling
// the file separately alongside the binary or duplicating its content
// in source.
package orgtd

import _ "embed"

//go:embed README.md
var Readme string
