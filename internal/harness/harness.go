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
	"strings"
)

// Harness is one supported AI host.
type Harness struct {
	Name        string // canonical id: "claude-code", "codex", "gemini-cli"
	DisplayName string // human label
	Detected    bool   // present on this machine
	BinaryPath  string // resolved binary path if found in $PATH
	SkillDir    string // user-level skill dir for this harness
	AgentDir    string // user-level subagent dir for this harness
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
		AgentDir:    filepath.Join(home, ".claude", "agents"),
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
		AgentDir: filepath.Join(home, ".codex", "agents"),
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
		AgentDir:    filepath.Join(home, ".gemini", "agents"),
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

// ProjectScoped returns a copy of h whose SkillDir and AgentDir are
// rebased from the user's home directory onto projectDir. For example a
// Claude Code SkillDir of ~/.claude/skills becomes
// <projectDir>/.claude/skills, and ~/.codex/agents becomes
// <projectDir>/.codex/agents. This is how `praxis refresh-skills
// --project` scopes an install to a single repo instead of the global
// user-level location. Detection state is preserved — only the write
// targets move. A directory that is not under the home dir is left
// unchanged.
func (h Harness) ProjectScoped(projectDir string) Harness {
	home, _ := os.UserHomeDir()
	h.SkillDir = rebaseUnderHome(home, projectDir, h.SkillDir)
	h.AgentDir = rebaseUnderHome(home, projectDir, h.AgentDir)
	return h
}

// rebaseUnderHome moves a home-relative path onto base. If p is not
// under home (or the relative computation fails), p is returned as-is.
func rebaseUnderHome(home, base, p string) string {
	if home == "" || p == "" {
		return p
	}
	rel, err := filepath.Rel(home, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.Join(base, rel)
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
