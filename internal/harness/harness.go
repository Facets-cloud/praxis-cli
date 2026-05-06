// Package harness detects which AI host CLIs/IDEs are present locally and
// reports where each one looks for skill files. The 3 harnesses listed
// here all support the Agent Skills open standard at user scope, so
// `praxis skill install` writes the same SKILL.md to each detected one.
//
// Cursor is intentionally NOT included: it has no user-scope skills
// directory (only project-scope under .cursor/skills/), so it requires
// per-repo handling and is deferred to a later release.
package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Harness is one supported AI host.
type Harness struct {
	Name        string // canonical id: "claude-code", "codex", "gemini-cli"
	DisplayName string // human label
	Detected    bool   // present on this machine
	BinaryPath  string // resolved binary path if found in $PATH
	SkillDir    string // user-level skill dir for this harness
}

// All returns every supported harness with its detection state filled in.
func All() []Harness {
	home, _ := os.UserHomeDir()
	return []Harness{
		detectClaudeCode(home),
		detectCodex(home),
		detectGeminiCLI(home),
	}
}

// Detected returns only the harnesses present on this machine.
func Detected() []Harness {
	out := make([]Harness, 0, 3)
	for _, h := range All() {
		if h.Detected {
			out = append(out, h)
		}
	}
	return out
}

// ByName returns the named harness regardless of detection state. The second
// return value is false if no harness matches the name.
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

func detectCodex(home string) Harness {
	h := Harness{
		Name:        "codex",
		DisplayName: "OpenAI Codex",
		// Codex's documented user-scope skill directory is ~/.agents/skills/
		// (the open Agent Skills standard alias).
		SkillDir: filepath.Join(home, ".agents", "skills"),
	}
	if p, err := exec.LookPath("codex"); err == nil {
		h.Detected = true
		h.BinaryPath = p
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); err == nil {
		h.Detected = true
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		h.Detected = true
	}
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

// String renders one line for status output.
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
