// Package skillinstall manages the lifecycle of skill files across the
// detected AI hosts on this machine. It writes SKILL.md files to each
// harness's user-scope skill directory and tracks the installations in
// a JSON receipt at ~/.praxis/installed.json.
package skillinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	Source      string    `json:"source,omitempty"`
	Scope       string    `json:"scope,omitempty"`
	Digest      string    `json:"digest,omitempty"`
}

// AgentInstallation is one (agent, harness, file) tuple — the unit
// recorded in the receipt for later list/uninstall of agent files.
type AgentInstallation struct {
	AgentName   string    `json:"agent_name"`
	Kind        string    `json:"kind"` // always "agent" today; retained for forward-compat
	Harness     string    `json:"harness"`
	Path        string    `json:"path"`
	InstalledAt time.Time `json:"installed_at"`
}

// Receipt is the on-disk format of installed.json. Skills and agents
// share the file; old skill-only receipts deserialize with Agents nil.
type Receipt struct {
	Skills []Installation      `json:"skills"`
	Agents []AgentInstallation `json:"agents,omitempty"`
}

// Install writes the named skill into every detected harness's user-level
// skill directory and records the installations in the receipt. Returns
// the per-host results in the order the hosts were given.
//
// The body comes from ContentFor — used for binary-embedded single-file
// skills (the "praxis" meta-skill in v0.x). Multi-file tree meta-skills are
// dispatched to InstallTree. For server-fetched org skills, use
// InstallWithBody instead.
func Install(skillName string, hosts []harness.Harness) ([]Installation, error) {
	w, err := embeddedWrite(skillName)
	if err != nil {
		return nil, err
	}
	var out []Installation
	err = withSkillLock(func() error { var err error; out, err = applyPackages([]packageWrite{w}, nil, hosts); return err })
	return out, err
}

// installToHosts writes a skill once per unique SkillDir (via write, called
// with <SkillDir>/<skillName>) and records an Installation under EVERY host,
// then saves the receipt. Hosts that share a SkillDir — Codex and Gemini both
// read ~/.agents/skills — resolve to the same dir, so the write runs once
// while status/list still report the skill as available to each harness.
// Writing per-harness would also needlessly churn (and briefly delete) a dir
// a sibling harness just populated, since the tree writers clear it first.
// The recorded path is always <dir>/SKILL.md.
func installToHosts(skillName string, hosts []harness.Harness, write func(dir string) error) ([]Installation, error) {
	var results []Installation
	err := withSkillLock(func() error {
		var err error
		results, err = applyPackages([]packageWrite{{name: skillName, source: "cli", write: write}}, nil, hosts)
		return err
	})
	return results, err
}

// InstallTree writes a multi-file (tree) skill into every host's skill
// directory, recreating the layout of fsys under <SkillDir>/<skillName>/.
// The canonical recorded path is the SKILL.md at the tree root, so
// list/status/uninstall treat a tree skill like any other installation.
// Used for binary-embedded multi-file meta-skills.
func InstallTree(skillName string, fsys fs.FS, hosts []harness.Harness) ([]Installation, error) {
	return installToHosts(skillName, hosts, func(dir string) error {
		return writeTree(fsys, dir)
	})
}

// writeTree replaces dstDir with the contents of fsys, recreating
// subdirectories. dstDir is cleared first so a re-install or Refresh from a
// binary whose embedded tree dropped or renamed a file does not leave the
// stale file behind — the on-disk tree always matches the embedded source.
func writeTree(fsys fs.FS, dstDir string) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != "." {
			if err := portablePath(p); err != nil {
				return err
			}
		}
		dst := filepath.Join(dstDir, filepath.FromSlash(p))
		if d.IsDir() {
			if err := os.MkdirAll(dst, 0700); err != nil {
				return fmt.Errorf("create %s: %w", dst, err)
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe skill file path %q: not regular", p)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
		}
		if err := os.WriteFile(dst, data, installedMode(p, info.Mode())); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
		return nil
	})
}

// InstallWithBody is like Install but takes the file body directly,
// bypassing ContentFor. Used by the catalog flow where the skill body
// arrives from the server's /v1/skills/bundle endpoint and isn't
// embedded in the binary.
func InstallWithBody(skillName, body string, hosts []harness.Harness) ([]Installation, error) {
	return installToHosts(skillName, hosts, func(dir string) error {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		path := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	})
}

// FileBody is one supporting file of a multi-file skill, ready to write:
// Path is a skill-relative POSIX path, Content the (already brand-substituted)
// body. Decouples skillinstall from skillcatalog's wire type.
type FileBody struct {
	Path    string
	Content string
}

// InstallTreeWithBodies installs a multi-file skill whose bodies arrive from
// the server catalog (not the binary embed): primary is the SKILL.md body
// (already rendered with the execution preamble), files are the supporting
// files. Like InstallTree it recreates the layout under <SkillDir>/<skillName>/
// and records the root SKILL.md as the canonical receipt path, so
// list/status/uninstall treat it like any other installation.
func InstallTreeWithBodies(skillName, primary string, files []FileBody, hosts []harness.Harness) ([]Installation, error) {
	return installToHosts(skillName, hosts, func(dir string) error {
		return writeBodies(dir, primary, files)
	})
}

// writeBodies replaces dstDir with a SKILL.md (primary) plus the given
// supporting files, recreating subdirectories. dstDir is cleared first so a
// re-install or refresh that dropped/renamed a file leaves nothing stale — the
// on-disk tree always matches the server's current set (mirrors writeTree).
//
// A supporting file whose path is absolute or escapes the skill dir (via "..")
// is rejected: the server validates this too, but the CLI must never write
// outside the skill folder on the strength of a server response.
func writeBodies(dstDir, primary string, files []FileBody) error {
	seen := map[string]bool{"skill.md": true}
	for _, f := range files {
		if err := portablePath(f.Path); err != nil {
			return err
		}
		key := strings.ToLower(f.Path)
		if seen[key] {
			return fmt.Errorf("duplicate skill file path %q", f.Path)
		}
		seen[key] = true
	}
	if err := os.MkdirAll(dstDir, 0700); err != nil {
		return fmt.Errorf("create %s: %w", dstDir, err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "SKILL.md"), []byte(primary), 0600); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Join(dstDir, "SKILL.md"), err)
	}
	for _, f := range files {
		dst := filepath.Join(dstDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
		}
		mode := fs.FileMode(0600)
		if strings.HasPrefix(f.Path, "scripts/") {
			mode = 0700
		}
		if err := os.WriteFile(dst, []byte(f.Content), mode); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}
	return nil
}

// Uninstall removes the named skill from every harness where the receipt
// shows it installed, deletes the file (and its parent skill dir if
// empty), and updates the receipt. Returns the entries that were
// actually removed.
func Uninstall(skillName string) ([]Installation, error) {
	return lockedInstallations(func() ([]Installation, error) { return uninstallLocked(skillName) })
}

func lockedInstallations(fn func() ([]Installation, error)) (out []Installation, err error) {
	err = withSkillLock(func() error { var e error; out, e = fn(); return e })
	return
}

func uninstallLocked(skillName string) ([]Installation, error) {
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
		if isTreeSkill(entry.SkillName) {
			// Multi-file skill: drop the whole tree dir.
			if err := os.RemoveAll(filepath.Dir(entry.Path)); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("remove %s: %w", filepath.Dir(entry.Path), err)
			}
		} else {
			if err := os.Remove(entry.Path); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("remove %s: %w", entry.Path, err)
			}
			// Best-effort: drop the parent skill dir if it's empty.
			_ = os.Remove(filepath.Dir(entry.Path))
		}
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
	return lockedInstallations(func() ([]Installation, error) { return uninstallByPrefixLocked(prefix) })
}

func uninstallByPrefixLocked(prefix string) ([]Installation, error) {
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
		// RemoveAll the whole skill dir, not just SKILL.md — a multi-file skill
		// would otherwise leave its supporting files orphaned on a profile
		// switch (the receipt path is always <skilldir>/SKILL.md).
		dir := filepath.Dir(entry.Path)
		if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove %s: %w", dir, err)
		}
		removed = append(removed, entry)
	}
	receipt.Skills = kept
	if err := saveReceipt(receipt); err != nil {
		return removed, fmt.Errorf("save receipt: %w", err)
	}
	return removed, nil
}

// RemoveOrphanedByPrefix removes on-disk skill directories matching
// `prefix` that are not tracked in the receipt and not present in `keep`.
// It is used after a successful catalog fetch to clean stale Praxis org
// skills that older installs may have left behind outside installed.json.
//
// RESERVED NAMESPACE CONTRACT: the `praxis-` prefix is owned by this CLI.
// Any directory under a harness skill dir whose name starts with `praxis-`
// is treated as CLI-managed: if it isn't in `installed.json`, not in the
// `keep` set, and not a meta-skill, it is removed via os.RemoveAll. That
// includes user-authored or third-party `praxis-foo` folders — callers
// should not pass `prefix="praxis-"` if user-authored namespacing is in
// play. In practice the only caller is `praxis login` post-auth setup,
// which seeds `keep` from the freshly-fetched catalog so any unrelated
// `praxis-*` folder on disk is, by definition, an orphan from an older
// install or a different profile.
//
// Meta-skills (anything in ContentFor) are PRESERVED — see UninstallByPrefix
// for the same exclusion logic.
func RemoveOrphanedByPrefix(prefix string, hosts []harness.Harness, keep map[string]bool) ([]Installation, error) {
	return lockedInstallations(func() ([]Installation, error) { return removeOrphanedByPrefixLocked(prefix, hosts, keep) })
}

func removeOrphanedByPrefixLocked(prefix string, hosts []harness.Harness, keep map[string]bool) ([]Installation, error) {
	if prefix == "" {
		return nil, fmt.Errorf("RemoveOrphanedByPrefix: prefix must be non-empty")
	}
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	recordedPaths := make(map[string]bool, len(receipt.Skills))
	for _, entry := range receipt.Skills {
		recordedPaths[filepath.Clean(entry.Path)] = true
	}

	now := time.Now().UTC()
	var removed []Installation
	for _, h := range hosts {
		entries, err := os.ReadDir(h.SkillDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("read %s: %w", h.SkillDir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasPrefix(name, prefix) || IsMetaSkill(name) || keep[name] {
				continue
			}
			path := filepath.Join(h.SkillDir, name, "SKILL.md")
			if recordedPaths[filepath.Clean(path)] {
				continue
			}
			dir := filepath.Join(h.SkillDir, name)
			if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
				return removed, fmt.Errorf("remove %s: %w", dir, err)
			}
			removed = append(removed, Installation{
				SkillName:   name,
				Harness:     h.Name,
				Path:        path,
				InstalledAt: now,
			})
		}
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

// Refresh rewrites every embedded skill in the receipt from this binary, one
// transaction per skill. Catalog skills are skipped: login and refresh-skills
// fetch those. Used after `praxis update`.
func Refresh() (refreshed []Installation, err error) {
	err = withSkillLock(func() error {
		receipt, err := loadReceipt()
		if err != nil {
			return err
		}
		var names []string
		hosts := map[string][]harness.Harness{}
		for _, e := range receipt.Skills {
			if !IsMetaSkill(e.SkillName) {
				continue
			}
			if _, ok := hosts[e.SkillName]; !ok {
				names = append(names, e.SkillName)
			}
			hosts[e.SkillName] = append(hosts[e.SkillName], harness.Harness{Name: e.Harness, SkillDir: filepath.Dir(filepath.Dir(e.Path))})
		}
		// One broken skill must not stop the others from refreshing.
		var errs []error
		for _, name := range names {
			w, err := embeddedWrite(name)
			if err == nil {
				var out []Installation
				out, err = applyPackages([]packageWrite{w}, nil, hosts[name])
				refreshed = append(refreshed, out...)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("refresh %s: %w", name, err))
			}
		}
		if _, err := retireLegacyLocked(hosts[praxisSkillName]); err != nil {
			errs = append(errs, fmt.Errorf("retire replaced skills: %w", err))
		}
		return errors.Join(errs...)
	})
	return
}

// embeddedWrite is the package write for a skill embedded in this binary.
func embeddedWrite(name string) (packageWrite, error) {
	tree, ok := treeSkillFS(name)
	if !ok {
		return packageWrite{}, fmt.Errorf("unknown skill %q (only embedded skills install from the binary)", name)
	}
	return packageWrite{name: name, source: "embedded", scope: "builtin", write: func(dir string) error { return writeTree(tree, dir) }}, nil
}

// RetireLegacyBuiltins removes the embedded skills that the praxis package
// replaced, on each host that now has praxis. Only receipt entries written by
// an embedded install (or by a praxis too old to record a source) qualify, so
// an org skill that reuses a legacy name is left alone. The engine backs up
// any tree it cannot prove unchanged.
func RetireLegacyBuiltins(hosts []harness.Harness) (retired []Installation, err error) {
	err = withSkillLock(func() error {
		var e error
		retired, e = retireLegacyLocked(hosts)
		return e
	})
	return
}

func retireLegacyLocked(hosts []harness.Harness) ([]Installation, error) {
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	var retire []Installation
	var where []harness.Harness
	seen := map[Installation]bool{}
	for _, h := range hosts {
		if _, err := os.Stat(filepath.Join(h.SkillDir, praxisSkillName, "SKILL.md")); err != nil {
			continue
		}
		where = append(where, h)
		for _, e := range receipt.Skills {
			if slices.Contains(legacyBuiltinSkills, e.SkillName) && (e.Source == "" || e.Source == "embedded") &&
				e.Path == filepath.Join(h.SkillDir, e.SkillName, "SKILL.md") && !seen[e] {
				seen[e] = true
				retire = append(retire, e)
			}
		}
	}
	if len(retire) == 0 {
		return nil, nil
	}
	if _, err := applyPackages(nil, retire, where); err != nil {
		return nil, err
	}
	return retire, nil
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

// installedMode is the mode writeTree gives an embedded file: scripts and
// executable files are 0700, everything else 0600.
func installedMode(p string, mode fs.FileMode) fs.FileMode {
	if mode.Perm()&0111 != 0 || strings.HasPrefix(p, "scripts/") {
		return 0700
	}
	return 0600
}

// embeddedDigest is the digest an install of embedded skill name records:
// hashTree's format over what writeTree would write, computed without writing.
func embeddedDigest(name string) (string, error) {
	tree, ok := treeSkillFS(name)
	if !ok {
		return "", fmt.Errorf("unknown skill %q (only embedded skills install from the binary)", name)
	}
	h := sha256.New()
	err := fs.WalkDir(tree, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			fmt.Fprintf(h, "dir:%s\x00", p)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := fs.ReadFile(tree, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "file:%s:%o:%d\x00", p, installedMode(p, info.Mode())&0111, len(b))
		_, _ = h.Write(b)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RefreshIfStale rewrites the embedded skills when the receipt records one
// that this binary would write differently — after a brew upgrade, a
// `praxis update` or a manual install. The receipt's digest is what was
// installed, so a skill the user edited does not trigger it. A receipt with no
// embedded skill is left alone: the first run installs it.
func RefreshIfStale() ([]Installation, error) {
	receipt, err := loadReceipt()
	if err != nil {
		return nil, err
	}
	want := map[string]string{}
	stale := false
	for _, e := range receipt.Skills {
		if !IsMetaSkill(e.SkillName) {
			continue
		}
		d, ok := want[e.SkillName]
		if !ok {
			if d, err = embeddedDigest(e.SkillName); err != nil {
				return nil, err
			}
			want[e.SkillName] = d
		}
		if e.Digest != d {
			stale = true
			break
		}
	}
	if !stale {
		return nil, nil
	}
	return Refresh()
}
