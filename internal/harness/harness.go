// Package harness detects which AI host CLIs/IDEs are present locally and
// reports where each one looks for skill/rule files. Phase 2 uses these
// SkillDir paths to install skill pointers.
package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Harness is one AI host (Claude Code, Cursor, Gemini CLI, …).
type Harness struct {
	Name        string // canonical id: "claude-code", "cursor", "gemini-cli"
	DisplayName string // human label
	Detected    bool   // any signal found on this machine
	BinaryPath  string // resolved binary path if found in $PATH
	SkillDir    string // where this harness expects skill files (may not exist)
}

// All returns the canonical list of supported harnesses with detection state.
func All() []Harness {
	home, _ := os.UserHomeDir()
	return []Harness{
		detectClaudeCode(home),
		detectCursor(home),
		detectGeminiCLI(home),
	}
}

// Detected returns only the harnesses present on this machine.
func Detected() []Harness {
	var out []Harness
	for _, h := range All() {
		if h.Detected {
			out = append(out, h)
		}
	}
	return out
}

// ByName returns the named harness (regardless of detection state).
func ByName(name string) (Harness, bool) {
	for _, h := range All() {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}

func detectClaudeCode(home string) Harness {
	h := Harness{
		Name:        "claude-code",
		DisplayName: "Claude Code",
		SkillDir:    filepath.Join(home, ".claude", "skills"),
	}
	if p, err := exec.LookPath("claude"); err == nil {
		h.Detected = true
		h.BinaryPath = p
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
		h.Detected = true
	}
	return h
}

func detectCursor(home string) Harness {
	h := Harness{
		Name:        "cursor",
		DisplayName: "Cursor",
		SkillDir:    filepath.Join(home, ".cursor", "rules"),
	}
	if p, err := exec.LookPath("cursor"); err == nil {
		h.Detected = true
		h.BinaryPath = p
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor")); err == nil {
		h.Detected = true
	}
	// Note: an installed-but-never-launched /Applications/Cursor.app on macOS
	// would not have ~/.cursor and is not a useful skill-install target until
	// the user has run Cursor at least once. Don't let its mere presence
	// inflate the detection signal.
	return h
}

func detectGeminiCLI(home string) Harness {
	h := Harness{
		Name:        "gemini-cli",
		DisplayName: "Gemini CLI",
		SkillDir:    filepath.Join(home, ".gemini", "skills"),
	}
	if p, err := exec.LookPath("gemini"); err == nil {
		h.Detected = true
		h.BinaryPath = p
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini")); err == nil {
		h.Detected = true
	}
	return h
}

// String renders one line for `praxis doctor`.
func (h Harness) String() string {
	state := "not detected"
	if h.Detected {
		state = "detected"
	}
	if h.BinaryPath != "" {
		return fmt.Sprintf("%s — %s (%s)", h.DisplayName, state, h.BinaryPath)
	}
	return fmt.Sprintf("%s — %s", h.DisplayName, state)
}
