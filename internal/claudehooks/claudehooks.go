// Package claudehooks merges praxis's SessionStart + CwdChanged hooks into a
// Claude Code settings.json. The hooks call `praxis ig hook <event>`, which
// nudges toward the use-ig skill when the session's cwd is a remembered ig
// catalog checkout (see internal/igcheckouts). settings.json hooks are a
// Claude-Code-specific mechanism, so only that host is wired.
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
	"path/filepath"
	"strings"
)

// hookEvent pairs a Claude settings key with the `praxis ig hook` event arg and
// the matcher Claude expects for that key.
type hookEvent struct {
	key, event, matcher string
}

func events() []hookEvent {
	return []hookEvent{
		{"SessionStart", "session-start", "startup|resume"},
		{"CwdChanged", "cwd-changed", ""},
	}
}

// command is the hook command string for praxisPath and event.
func command(praxisPath, event string) string {
	return praxisPath + " ig hook " + event
}

// isPraxisHookCommand reports whether cmd is OUR hook for event. The event
// suffix alone is insufficient — another tool could ship `foo ig hook
// session-start` — so argv[0]'s basename must actually be praxis. Getting this
// wrong would clobber a foreign hook.
func isPraxisHookCommand(cmd, event string) bool {
	if !strings.HasSuffix(cmd, " ig hook "+event) {
		return false
	}
	fields := strings.Fields(cmd)
	return len(fields) > 0 && filepath.Base(fields[0]) == "praxis"
}

// listUpsert ensures list holds exactly one praxis entry for event pointing at
// praxisPath. An entry from an older install path is refreshed in place.
func listUpsert(list []any, praxisPath, event, matcher string) ([]any, bool) {
	want := command(praxisPath, event)
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
			h, ok := hv.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := h["command"].(string)
			if !isPraxisHookCommand(cmd, event) {
				continue
			}
			if cmd == want {
				return list, false // ours, already current
			}
			h["command"] = want // stale binary path → refresh in place
			return list, true
		}
	}
	entry := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": want, "timeout": 5}}}
	if matcher != "" {
		entry["matcher"] = matcher
	}
	return append(list, entry), true
}

// listRemove strips every praxis entry for event from list. An entry is dropped
// only when removing our command empties its inner hooks; a mixed entry keeps
// its foreign hooks.
func listRemove(list []any, event string) ([]any, bool) {
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
				if cmd, _ := h["command"].(string); isPraxisHookCommand(cmd, event) {
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

// mutate loads settingsPath, applies fn to its hooks map, and writes back if
// fn reported a change. A missing file is treated as empty. Invalid JSON is an
// error (we refuse to overwrite a file we can't parse). The prior file is kept
// as settings.json.bak.
func mutate(settingsPath string, fn func(hooks map[string]any) bool) (bool, error) {
	raw, err := os.ReadFile(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	settings := map[string]any{}
	if len(raw) > 0 {
		if uErr := json.Unmarshal(raw, &settings); uErr != nil {
			return false, fmt.Errorf("hooks: %s is not valid JSON (refusing to overwrite): %w", settingsPath, uErr)
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
		_ = os.WriteFile(settingsPath+".bak", raw, 0o644)
	}
	b, mErr := json.MarshalIndent(settings, "", "  ")
	if mErr != nil {
		return false, mErr
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(settingsPath, append(b, '\n'), 0o644)
}

// Install merges praxis's SessionStart + CwdChanged hooks into settingsPath,
// pointing at praxisPath. Returns whether the file changed.
func Install(settingsPath, praxisPath string) (bool, error) {
	return mutate(settingsPath, func(hooks map[string]any) bool {
		changed := false
		for _, e := range events() {
			list, _ := hooks[e.key].([]any)
			next, ch := listUpsert(list, praxisPath, e.event, e.matcher)
			if ch {
				hooks[e.key] = next
				changed = true
			}
		}
		return changed
	})
}

// Uninstall removes praxis's SessionStart + CwdChanged hooks from settingsPath,
// leaving foreign hooks and other keys intact. Returns whether the file changed.
func Uninstall(settingsPath, praxisPath string) (bool, error) {
	return mutate(settingsPath, func(hooks map[string]any) bool {
		changed := false
		for _, e := range events() {
			list, ok := hooks[e.key].([]any)
			if !ok {
				continue
			}
			next, ch := listRemove(list, e.event)
			if ch {
				if len(next) == 0 {
					delete(hooks, e.key)
				} else {
					hooks[e.key] = next
				}
				changed = true
			}
		}
		return changed
	})
}
