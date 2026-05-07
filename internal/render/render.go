// Package render handles output mode (pretty vs JSON) for praxis commands.
//
// Praxis is operated by AI hosts (Claude Code, Codex, Gemini CLI, …) more
// than by humans. The default output mode auto-detects:
//
//   - stdout is a TTY (interactive shell) → pretty text
//   - stdout is not a TTY (pipe / subprocess) → JSON
//
// An explicit --json flag forces JSON. An explicit --pretty flag forces
// text.
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// IsTTY reports whether w is a terminal that humans are likely to read.
// Non-files (in-memory buffers used in tests) are treated as non-TTY.
func IsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// UseJSON resolves the effective output mode given the --json/--pretty
// flags and the writer. AI hosts get JSON automatically when piping.
func UseJSON(jsonFlag, prettyFlag bool, w io.Writer) bool {
	if prettyFlag {
		return false
	}
	if jsonFlag {
		return true
	}
	return !IsTTY(w)
}

// JSON writes v to w as pretty-indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Error is the JSON shape returned for failures so AI hosts can dispatch
// on the structured fields rather than parsing English.
type Error struct {
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
	Code  int    `json:"code,omitempty"`
}

// PrintError emits an error in JSON or text form depending on asJSON.
func PrintError(w io.Writer, asJSON bool, msg, hint string, code int) {
	if asJSON {
		_ = JSON(w, Error{Error: msg, Hint: hint, Code: code})
		return
	}
	fmt.Fprintf(w, "Error: %s\n", msg)
	if hint != "" {
		fmt.Fprintf(w, "Hint: %s\n", hint)
	}
}
