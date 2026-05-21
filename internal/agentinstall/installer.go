// Package agentinstall manages the lifecycle of agent files across
// the detected AI hosts on this machine. It writes per-harness files
// (`.md` for Claude / Gemini, `.toml` for Codex) into each host's
// user-scope `agents/` directory and records the installations in
// the shared receipt at ~/.praxis/installed.json.
package agentinstall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// Install renders every agent for every detected harness, writes the
// resulting file, and records each (agent, harness, path) tuple in
// the receipt. Returns the per-host installations in the order they
// were written.
//
// Skips:
//   - undetected harnesses (Detected == false)
//   - harnesses whose AgentDir is empty
//   - (agent, harness) pairs where Render returns an error (e.g. Codex
//   - system_prompt containing triple-quotes). Batch continues.
func Install(agents []agentcatalog.Agent, hosts []harness.Harness) ([]skillinstall.AgentInstallation, error) {
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	results := make([]skillinstall.AgentInstallation, 0, len(agents)*len(hosts))
	for _, a := range agents {
		for _, h := range hosts {
			if !h.Detected || h.AgentDir == "" {
				continue
			}
			if !supportsAgentInstall(h.Name) {
				continue
			}
			body, err := a.Render(h.Name)
			if err != nil {
				continue
			}
			ext := extensionFor(h.Name)
			fileName := a.PrefixedName() + ext
			if err := os.MkdirAll(h.AgentDir, 0700); err != nil {
				return results, fmt.Errorf("create %s: %w", h.AgentDir, err)
			}
			fullPath, err := atomicWriteFile(h.AgentDir, fileName, []byte(body), 0600)
			if err != nil {
				return results, fmt.Errorf("write %s: %w", filepath.Join(h.AgentDir, fileName), err)
			}
			install := skillinstall.AgentInstallation{
				AgentName:   a.PrefixedName(),
				Kind:        a.Kind,
				Harness:     h.Name,
				Path:        fullPath,
				InstalledAt: now,
			}
			results = append(results, install)
			receipt = upsert(receipt, install)
		}
	}

	if err := saveReceipt(receipt); err != nil {
		return results, fmt.Errorf("save receipt: %w", err)
	}
	return results, nil
}

// UninstallByPrefix removes every recorded agent whose AgentName starts
// with `prefix`. Used by login (wipe previous profile's `praxis-*`
// agents before installing the new profile's set) and logout.
func UninstallByPrefix(prefix string) ([]skillinstall.AgentInstallation, error) {
	if prefix == "" {
		return nil, fmt.Errorf("UninstallByPrefix: prefix must be non-empty")
	}
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	var removed []skillinstall.AgentInstallation
	var kept []skillinstall.AgentInstallation
	var removeErrs []string
	for _, entry := range receipt.Agents {
		if !strings.HasPrefix(entry.AgentName, prefix) {
			kept = append(kept, entry)
			continue
		}
		if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
			// File couldn't be removed — keep the receipt entry so a
			// retry can pick it up later. Continue the batch so other
			// removals + the receipt save still happen.
			removeErrs = append(removeErrs, fmt.Sprintf("%s: %v", entry.Path, err))
			kept = append(kept, entry)
			continue
		}
		removed = append(removed, entry)
	}
	receipt.Agents = kept
	if err := saveReceipt(receipt); err != nil {
		return removed, fmt.Errorf("save receipt: %w", err)
	}
	if len(removeErrs) > 0 {
		return removed, fmt.Errorf("removed %d agent file(s); %d failed: %s",
			len(removed), len(removeErrs), strings.Join(removeErrs, "; "))
	}
	return removed, nil
}

// RemoveOrphanedByPrefix removes on-disk agent files matching `prefix`
// in each host's AgentDir that aren't recorded in the receipt and
// aren't in the `keep` set. Mirrors skillinstall.RemoveOrphanedByPrefix
// for the agent side — cleans up files from older praxis-cli versions
// or from gates that have toggled (e.g. files left behind in
// ~/.codex/agents/ when Codex is gated off).
//
// RESERVED NAMESPACE CONTRACT: the `praxis-` prefix is owned by this
// CLI. Any file under a harness AgentDir whose name starts with
// `praxis-` and isn't in the receipt or the keep set is removed.
// Callers should not pass `prefix="praxis-"` if user-authored
// namespacing is in play.
//
// `keep` carries the PrefixedName() of agents we want to retain.
// In the post-auth flow that's the fresh-fetch's PrefixedName() list;
// any praxis-* file not in that list (and not in the receipt) is an
// orphan from a previous install or a gated host.
func RemoveOrphanedByPrefix(prefix string, hosts []harness.Harness, keep map[string]bool) ([]skillinstall.AgentInstallation, error) {
	if prefix == "" {
		return nil, fmt.Errorf("RemoveOrphanedByPrefix: prefix must be non-empty")
	}
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	recordedPaths := make(map[string]bool, len(receipt.Agents))
	for _, entry := range receipt.Agents {
		recordedPaths[filepath.Clean(entry.Path)] = true
	}

	now := time.Now().UTC()
	var removed []skillinstall.AgentInstallation
	for _, h := range hosts {
		if h.AgentDir == "" {
			continue
		}
		entries, err := os.ReadDir(h.AgentDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("read %s: %w", h.AgentDir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			// Strip the per-host extension (.md / .toml) to get the
			// canonical PrefixedName for the keep-set check.
			base := name
			if ext := extensionFor(h.Name); strings.HasSuffix(base, ext) {
				base = base[:len(base)-len(ext)]
			}
			if keep[base] {
				continue
			}
			path := filepath.Join(h.AgentDir, name)
			if recordedPaths[filepath.Clean(path)] {
				continue
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("remove %s: %w", path, err)
			}
			removed = append(removed, skillinstall.AgentInstallation{
				AgentName:   base,
				Kind:        "agent",
				Harness:     h.Name,
				Path:        path,
				InstalledAt: now,
			})
		}
	}
	return removed, nil
}

// List returns every agent currently recorded in the receipt.
func List() ([]skillinstall.AgentInstallation, error) {
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	// Normalize empty-kind entries to "agent" so callers can switch
	// on Kind without nil checks.
	for i := range receipt.Agents {
		if receipt.Agents[i].Kind == "" {
			receipt.Agents[i].Kind = agentcatalog.KindAgent
		}
	}
	return receipt.Agents, nil
}

// atomicWriteFile writes `data` to `dir/name` via a same-dir temp file +
// fsync + rename, so a concurrent reader / crash mid-write cannot
// observe a partial file. The temp file is removed on any failure
// before the rename. On success the final file has mode `perm`.
//
// Same pattern as saveReceipt below, lifted to a helper so the Install
// loop and the receipt save share one source of truth on atomicity.
func atomicWriteFile(dir, name string, data []byte, perm os.FileMode) (string, error) {
	fullPath := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, fullPath); err != nil {
		return "", err
	}
	cleanup = false
	return fullPath, nil
}

// supportsAgentInstall reports whether a detected host has been
// runtime-verified to load praxis-* agent files from its AgentDir.
//
// Verified: Claude Code (`Task` tool dispatch against
// ~/.claude/agents/*.md), and Gemini CLI (loaded agents surface via
// `@<name>` invocation and auto-routing from ~/.gemini/agents/*.md).
//
// Not verified: Codex. The TOML format the renderer produces
// (`name`, `description`, `developer_instructions = """..."""`)
// matches what the Codex subagents docs prescribe exactly, but a
// runtime smoke against Codex did not surface the installed agents.
// The docs themselves note "the format may evolve as authoring and
// sharing mature." Skip Codex until its loader consumes the
// documented format — flipping this function re-enables it.
//
// The renderer and extensionFor logic already understand Codex;
// only this gate decides whether the file gets written.
func supportsAgentInstall(harnessName string) bool {
	switch harnessName {
	case "claude-code", "gemini-cli":
		return true
	default:
		return false
	}
}

// extensionFor returns the per-harness file extension. Claude Code
// and Gemini CLI use `.md`; Codex uses `.toml`.
func extensionFor(harnessName string) string {
	if harnessName == "codex" {
		return ".toml"
	}
	return ".md"
}

func upsert(r skillinstall.Receipt, in skillinstall.AgentInstallation) skillinstall.Receipt {
	for i, e := range r.Agents {
		if e.AgentName == in.AgentName && e.Harness == in.Harness {
			r.Agents[i] = in
			return r
		}
	}
	r.Agents = append(r.Agents, in)
	return r
}

func loadReceipt() (skillinstall.Receipt, error) {
	path, err := paths.Installed()
	if err != nil {
		return skillinstall.Receipt{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return skillinstall.Receipt{}, nil
		}
		return skillinstall.Receipt{}, fmt.Errorf("read %s: %w", path, err)
	}
	var r skillinstall.Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return skillinstall.Receipt{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return r, nil
}

// saveReceipt persists the receipt via atomicWriteFile so writes go
// through the same temp-file + fsync + rename path as agent file
// writes. Previous hand-rolled implementation here was missing the
// fsync between Write and Close, leaving a window where a crash
// could persist file metadata without the data — the shared helper
// closes that gap and eliminates the duplicate code.
func saveReceipt(r skillinstall.Receipt) error {
	path, err := paths.Installed()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = atomicWriteFile(filepath.Dir(path), filepath.Base(path), data, 0600)
	return err
}
