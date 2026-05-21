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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".installed-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
