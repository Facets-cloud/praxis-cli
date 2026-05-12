// Package skillinstall manages the lifecycle of skill files across the
// detected AI hosts on this machine. It writes SKILL.md files to each
// harness's user-scope skill directory and tracks the installations in
// a JSON receipt at ~/.praxis/installed.json.
package skillinstall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// Installation is one (skill, harness, file) tuple — the unit recorded in
// the receipt for later list/uninstall.
type Installation struct {
	SkillName   string    `json:"skill_name"`
	Harness     string    `json:"harness"`
	Path        string    `json:"path"`
	InstalledAt time.Time `json:"installed_at"`
}

// Receipt is the on-disk format of installed.json.
type Receipt struct {
	Skills []Installation `json:"skills"`
}

// Install writes the named skill into every detected harness's user-level
// skill directory and records the installations in the receipt. Returns
// the per-host results in the order the hosts were given.
//
// The body comes from ContentFor — used for binary-embedded skills (the
// "praxis" meta-skill in v0.x). For server-fetched org skills, use
// InstallWithBody instead.
func Install(skillName string, hosts []harness.Harness) ([]Installation, error) {
	body, err := ContentFor(skillName)
	if err != nil {
		return nil, err
	}
	return InstallWithBody(skillName, body, hosts)
}

// InstallWithBody is like Install but takes the file body directly,
// bypassing ContentFor. Used by the catalog flow where the skill body
// arrives from the server's /v1/skills/bundle endpoint and isn't
// embedded in the binary.
func InstallWithBody(skillName, body string, hosts []harness.Harness) ([]Installation, error) {
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	results := make([]Installation, 0, len(hosts))
	for _, h := range hosts {
		dir := filepath.Join(h.SkillDir, skillName)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return results, fmt.Errorf("create %s: %w", dir, err)
		}
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			return results, fmt.Errorf("write %s: %w", path, err)
		}
		install := Installation{
			SkillName:   skillName,
			Harness:     h.Name,
			Path:        path,
			InstalledAt: now,
		}
		results = append(results, install)
		receipt = upsert(receipt, install)
	}

	if err := saveReceipt(receipt); err != nil {
		return results, fmt.Errorf("save receipt: %w", err)
	}
	return results, nil
}

// Uninstall removes the named skill from every harness where the receipt
// shows it installed, deletes the file (and its parent skill dir if
// empty), and updates the receipt. Returns the entries that were
// actually removed.
func Uninstall(skillName string) ([]Installation, error) {
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	var removed []Installation
	var kept []Installation
	for _, entry := range receipt.Skills {
		if entry.SkillName != skillName {
			kept = append(kept, entry)
			continue
		}
		if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove %s: %w", entry.Path, err)
		}
		// Best-effort: drop the parent skill dir if it's empty.
		_ = os.Remove(filepath.Dir(entry.Path))
		removed = append(removed, entry)
	}
	receipt.Skills = kept
	if err := saveReceipt(receipt); err != nil {
		return removed, fmt.Errorf("save receipt: %w", err)
	}
	return removed, nil
}

// UninstallByPrefix removes every installation whose skill name starts
// with `prefix`, deleting files (and empty parent dirs) and updating
// the receipt. Returns the entries that were actually removed.
//
// Used by `praxis login` (to wipe the previous profile's org skills
// before installing the new profile's catalog) and `praxis logout`
// (to remove org skills alongside credentials).
//
// Meta-skills (anything in ContentFor) are PRESERVED even when their
// name matches the prefix — e.g. "praxis-memory" starts with "praxis-"
// but is a binary-embedded meta-skill and must survive profile
// switches. The legacy meta-skill "praxis" (no suffix) survives the
// `"praxis-"` prefix naturally; this exclusion handles new
// prefix-shaped meta-skills as they're added.
func UninstallByPrefix(prefix string) ([]Installation, error) {
	if prefix == "" {
		return nil, fmt.Errorf("UninstallByPrefix: prefix must be non-empty")
	}
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	var removed []Installation
	var kept []Installation
	for _, entry := range receipt.Skills {
		if !strings.HasPrefix(entry.SkillName, prefix) || IsMetaSkill(entry.SkillName) {
			kept = append(kept, entry)
			continue
		}
		if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove %s: %w", entry.Path, err)
		}
		_ = os.Remove(filepath.Dir(entry.Path))
		removed = append(removed, entry)
	}
	receipt.Skills = kept
	if err := saveReceipt(receipt); err != nil {
		return removed, fmt.Errorf("save receipt: %w", err)
	}
	return removed, nil
}

// List returns every installation currently recorded in the receipt.
func List() ([]Installation, error) {
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	return receipt.Skills, nil
}

// Refresh re-writes the SKILL.md for every installation in the receipt
// using the current ContentFor(). Used after `praxis update` to pick up
// new skill content, and exposed as `praxis refresh-skills` for manual
// invocation. Entries whose skill no longer exists in ContentFor are
// skipped (not removed) so a future update reintroducing the skill can
// repopulate them. Returns the entries actually refreshed.
func Refresh() ([]Installation, error) {
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	refreshed := make([]Installation, 0, len(receipt.Skills))
	for i, entry := range receipt.Skills {
		body, err := ContentFor(entry.SkillName)
		if err != nil {
			// Skill no longer in catalog — leave the file alone, skip.
			continue
		}
		if err := os.MkdirAll(filepath.Dir(entry.Path), 0700); err != nil {
			return refreshed, fmt.Errorf("ensure dir for %s: %w", entry.Path, err)
		}
		if err := os.WriteFile(entry.Path, []byte(body), 0600); err != nil {
			return refreshed, fmt.Errorf("refresh %s: %w", entry.Path, err)
		}
		receipt.Skills[i].InstalledAt = now
		refreshed = append(refreshed, receipt.Skills[i])
	}
	if err := saveReceipt(receipt); err != nil {
		return refreshed, fmt.Errorf("save receipt: %w", err)
	}
	return refreshed, nil
}

// upsert replaces an existing (skill, harness) entry or appends a new one.
func upsert(r Receipt, in Installation) Receipt {
	for i, e := range r.Skills {
		if e.SkillName == in.SkillName && e.Harness == in.Harness {
			r.Skills[i] = in
			return r
		}
	}
	r.Skills = append(r.Skills, in)
	return r
}

func loadReceipt() (Receipt, error) {
	path, err := paths.Installed()
	if err != nil {
		return Receipt{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Receipt{}, nil
		}
		return Receipt{}, fmt.Errorf("read %s: %w", path, err)
	}
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return Receipt{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return r, nil
}

// saveReceipt writes the receipt atomically: temp file + rename so a
// crash mid-write doesn't leave a corrupt JSON.
func saveReceipt(r Receipt) error {
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
