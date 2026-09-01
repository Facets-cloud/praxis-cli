// Package claudehooks merges praxis's SessionStart + CwdChanged hooks into a
// Claude Code settings.json. The hooks call `praxis ig hook <event>`, which
// nudges toward the use-ig skill when the session's cwd is a repo the catalog
// server claims as an ig member (membership is resolved from the repo's
// canonical git identity — see cmd `praxis ig hook`; no agent-maintained file is
// consulted), and `praxis hook user-prompt-submit`, which nudges toward the
// skill matching the submitted prompt.
//
// Claude Code gets all three events. Codex and Gemini CLI get the prompt hook
// only, into their own files — see Hosts() for the per-host keys and units.
//
// The merge is additive and idempotent: other hooks and top-level keys are left
// untouched, exactly one praxis entry exists per event, and a moved praxis
// binary refreshes the command in place rather than duplicating it. The pattern
// mirrors ig's own `ig skills install` hook wiring.
package claudehooks

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hookEvent pairs a host's settings key with the praxis argv to run and the
// matcher that host expects.
type hookEvent struct {
	key, args, matcher string
}

// Host is one AI host's hook config. The differences are not guessable: Gemini
// calls the prompt event BeforeAgent and takes its timeout in MILLISECONDS,
// Codex keeps hooks in its own hooks.json and will not run one until trusted
// via its in-app /hooks, and Antigravity has no hook mechanism at all.
type Host struct {
	Harness   string
	File      string
	Events    []hookEvent
	Timeout   int
	EntryName string // Gemini lists entries by name in /hooks; others have no such field
}

const promptArgs = "hook user-prompt-submit"

// allArgs is every hook praxis has ever written, for Uninstall to sweep.
var allArgs = []string{"ig hook session-start", "ig hook cwd-changed", promptArgs}

func Hosts(home string) []Host {
	return []Host{
		{Harness: "claude-code", File: filepath.Join(home, ".claude", "settings.json"), Timeout: 5, Events: []hookEvent{
			{"SessionStart", "ig hook session-start", "startup|resume"},
			{"CwdChanged", "ig hook cwd-changed", ""},
			{"UserPromptSubmit", promptArgs, ""},
		}},
		{Harness: "codex", File: filepath.Join(home, ".codex", "hooks.json"), Timeout: 5, Events: []hookEvent{
			{"UserPromptSubmit", promptArgs, ""},
		}},
		{Harness: "gemini-cli", File: filepath.Join(home, ".gemini", "settings.json"), Timeout: 5000,
			EntryName: "praxis-skill-nudge", Events: []hookEvent{
				{"BeforeAgent", promptArgs, ""},
			}},
		{Harness: "antigravity"}, // no hook mechanism; listed so callers can say so
	}
}

// ByHarness returns the host with the given harness id.
func ByHarness(home, harness string) (Host, bool) {
	for _, h := range Hosts(home) {
		if h.Harness == harness {
			return h, true
		}
	}
	return Host{}, false
}

// command is the hook command string for praxisPath and args. The executable
// path is shell-quoted so a path containing spaces (e.g. "/Applications/Praxis
// CLI/praxis") still runs — Claude Code executes the command via a shell.
func command(praxisPath, args string) string {
	return shellQuote(praxisPath) + " " + args
}

// shellQuote single-quotes s for safe use as one shell word, escaping any
// embedded single quote.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookExecBase returns the basename of a hook command's argv[0], handling both
// our shell-quoted form and a bare (older, unquoted) install — so an upgrade
// recognizes and refreshes the prior entry rather than duplicating it.
func hookExecBase(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	var tok string
	if cmd[0] == '\'' {
		if end := strings.IndexByte(cmd[1:], '\''); end >= 0 {
			tok = cmd[1 : 1+end]
		} else {
			tok = cmd[1:]
		}
	} else {
		tok = strings.Fields(cmd)[0]
	}
	return filepath.Base(tok)
}

// isPraxisExec reports whether a hook argv[0] basename is praxis. Release
// binaries keep their per-arch asset name (praxis_darwin_arm64), and a
// Homebrew cask renames only the symlink in bin/ — so entries written from the
// staged file carry that name and must still be recognized as ours.
func isPraxisExec(base string) bool {
	return base == "praxis" || strings.HasPrefix(base, "praxis_")
}

// isPraxisHookCommand reports whether cmd is OUR hook for args. The argv
// suffix alone is insufficient — another tool could ship `foo ig hook
// session-start` — so argv[0]'s basename must actually be praxis. Getting this
// wrong would clobber a foreign hook.
func isPraxisHookCommand(cmd, args string) bool {
	if !strings.HasSuffix(cmd, " "+args) {
		return false
	}
	return isPraxisExec(hookExecBase(cmd))
}

// StableBinaryPath is the PATH entry that resolves to the running binary, and
// whether one exists. os.Executable reports the VERSION-STAMPED install
// directory (Homebrew stages the binary in Caskroom/<version>/), which the next
// upgrade deletes — taking every hook wired from it with it. The cask's
// bin/praxis symlink survives upgrades, so a hook must name that instead. Never
// resolve symlinks here: that is exactly what pins a hook to one version.
//
// A praxis elsewhere on PATH is a DIFFERENT install, so the candidate must be
// the same file. Repair uses the ok result to stay out of a host's config
// unless it has a durable path to offer.
func StableBinaryPath() (string, bool) {
	exe, err := execPathForTest() // absolute on darwin and linux, the two we build
	if err != nil {
		return "", false
	}
	stable, err := exec.LookPath("praxis")
	if err != nil {
		return "", false
	}
	if abs, aErr := filepath.Abs(stable); aErr == nil {
		stable = abs // a "." PATH entry yields a relative path
	}
	if !sameFile(stable, exe) {
		return "", false
	}
	return stable, true
}

// BinaryPath is the praxis path to bake into a hook command: the stable PATH
// entry, else the running binary. `praxis login` accepts the fallback because
// the user chose which binary to run.
func BinaryPath() (string, error) {
	if stable, ok := StableBinaryPath(); ok {
		return stable, nil
	}
	return execPathForTest()
}

// execPathForTest is os.Executable, seamed for tests.
var execPathForTest = os.Executable

// SetExecPathForTest pins the running-binary path and returns the restore func,
// so a test can stage a cask layout without an installed praxis.
func SetExecPathForTest(path string) func() {
	prev := execPathForTest
	execPathForTest = func() (string, error) { return path, nil }
	return func() { execPathForTest = prev }
}

// sameFile reports whether two paths name one file, following symlinks.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// praxisCommandsFor returns every praxis hook command string for args in list.
func praxisCommandsFor(list []any, args string) []string {
	var out []string
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := entry["hooks"].([]any)
		if !ok {
			continue
		}
		for _, hv := range inner {
			if h, ok := hv.(map[string]any); ok {
				if cmd, _ := h["command"].(string); isPraxisHookCommand(cmd, args) {
					out = append(out, cmd)
				}
			}
		}
	}
	return out
}

// listUpsert normalizes list to hold EXACTLY ONE praxis entry for e pointing
// at praxisPath: it is a no-op when that already holds, otherwise it strips every
// praxis hook for the event (foreign hooks preserved) and appends one fresh
// entry — collapsing stale-path or accidentally-duplicated entries.
func listUpsert(list []any, h Host, praxisPath string, e hookEvent) ([]any, bool) {
	want := command(praxisPath, e.args)
	if found := praxisCommandsFor(list, e.args); len(found) == 1 && found[0] == want {
		return list, false // already exactly one, and current
	}
	stripped, _ := listRemove(list, e.args)
	inner := map[string]any{"type": "command", "command": want, "timeout": h.Timeout}
	if h.EntryName != "" {
		inner["name"] = h.EntryName
	}
	entry := map[string]any{"hooks": []any{inner}}
	if e.matcher != "" {
		entry["matcher"] = e.matcher
	}
	return append(stripped, entry), true
}

// listRemove strips every praxis entry for args from list. An entry is dropped
// only when removing our command empties its inner hooks; a mixed entry keeps
// its foreign hooks.
func listRemove(list []any, args string) ([]any, bool) {
	changed := false
	out := make([]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		inner, ok := entry["hooks"].([]any)
		if !ok {
			out = append(out, item)
			continue
		}
		kept := make([]any, 0, len(inner))
		for _, hv := range inner {
			h, ok := hv.(map[string]any)
			if ok {
				if cmd, _ := h["command"].(string); isPraxisHookCommand(cmd, args) {
					changed = true
					continue
				}
			}
			kept = append(kept, hv)
		}
		if len(kept) == 0 {
			continue // entry existed only for our hook — drop it
		}
		entry["hooks"] = kept
		out = append(out, entry)
	}
	return out, changed
}

// mutate loads path, applies fn to its hooks map, and writes back if
// fn reported a change. A missing file is treated as empty. Invalid JSON is an
// error (we refuse to overwrite a file we can't parse). The prior file is kept
// as settings.json.bak. Backup and any newly-created settings file are written
// 0600 — a Claude settings file can hold credentials/env values, so its copy
// must not be world-readable.
func mutate(path string, fn func(hooks map[string]any) bool) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	settings := map[string]any{}
	if len(raw) > 0 {
		if uErr := json.Unmarshal(raw, &settings); uErr != nil {
			return false, fmt.Errorf("hooks: %s is not valid JSON (refusing to overwrite): %w", path, uErr)
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	if !fn(hooks) {
		return false, nil
	}
	settings["hooks"] = hooks
	if len(raw) > 0 {
		if bErr := os.WriteFile(path+".bak", raw, 0o600); bErr != nil {
			return false, fmt.Errorf("hooks: writing backup %s.bak: %w", path, bErr)
		}
	}
	b, mErr := json.MarshalIndent(settings, "", "  ")
	if mErr != nil {
		return false, mErr
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(b, '\n'), 0o600)
}

// Install merges h's hooks into its config file, pointing at praxisPath.
// Returns whether the file changed. A host with no events is a no-op.
func Install(h Host, praxisPath string) (bool, error) {
	return upsert(h, praxisPath, false)
}

// Repair re-points h's EXISTING praxis hook entries at praxisPath and adds
// none. An upgrade deletes the directory an older praxis wired its hooks from,
// so `praxis setup` (the Homebrew post-install hook) calls this to heal them
// without a login — while a user who never logged in still gets no hooks.
func Repair(h Host, praxisPath string) (bool, error) {
	return upsert(h, praxisPath, true)
}

// upsert points h's hooks at praxisPath. When existingOnly, an event that
// carries no praxis hook today is left alone. Install and Repair share this so
// they cannot drift on the empty-host guard or the write path.
func upsert(h Host, praxisPath string, existingOnly bool) (bool, error) {
	if len(h.Events) == 0 {
		return false, nil // a host with no hook mechanism has no file to write
	}
	return mutate(h.File, func(hooks map[string]any) bool {
		changed := false
		for _, e := range h.Events {
			list, _ := hooks[e.key].([]any)
			if existingOnly && len(praxisCommandsFor(list, e.args)) == 0 {
				continue
			}
			next, ch := listUpsert(list, h, praxisPath, e)
			if ch {
				hooks[e.key] = next
				changed = true
			}
		}
		return changed
	})
}

// Uninstall removes praxis's hooks from h's config file, leaving foreign hooks
// and other keys intact. It sweeps every key present in the file, not just the
// ones h declares, so a key an older praxis wrote is still cleaned up.
func Uninstall(h Host) (bool, error) {
	return mutate(h.File, func(hooks map[string]any) bool {
		changed := false
		for key, v := range hooks {
			list, ok := v.([]any)
			if !ok {
				continue
			}
			hit := false
			for _, args := range allArgs {
				if next, ch := listRemove(list, args); ch {
					list, hit = next, true
				}
			}
			if !hit {
				continue
			}
			changed = true
			if len(list) == 0 {
				delete(hooks, key)
			} else {
				hooks[key] = list
			}
		}
		return changed
	})
}
