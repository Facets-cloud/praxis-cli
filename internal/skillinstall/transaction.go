package skillinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
)

// A transaction stages every destination before changing any discoverable
// package. The receipt is its commit point. Backups live outside skill roots.
type packageWrite struct {
	name, source, scope string
	write               func(string) error
	mustBeAbsent        bool
}

type treeChange struct {
	dest, stage, old, workspace string
	hadOld, activated           bool
	priorDigest                 string
	priorInfo, activeInfo       fs.FileInfo
	activeDigest                string
	mustBeAbsent                bool
}

var renameNewTree = renameNoReplace

// Skill writers share an advisory process lock, including across CLI processes.
// The lock file is persistent: unlinking it would let a waiter lock a new inode.
// WithReceiptLock extends the same non-reentrant lock to agent lifecycle writers.
// Hold it across read-modify-save, not only the final atomic rename. Callbacks
// must not call public skill lifecycle operations (which acquire it themselves).
func WithReceiptLock(fn func() error) error { return withSkillLock(fn) }

func withSkillLock(fn func() error) error {
	root, err := paths.ActiveRoot()
	if err != nil {
		return err
	}
	if err := noSymlinkPath(root); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	p := filepath.Join(root, ".skills.lock")
	if err := noSymlinkPath(p); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock skills: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func portablePath(p string) error {
	if !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, "\\:\x00") {
		return fmt.Errorf("unsafe skill file path %q", p)
	}
	for _, part := range strings.Split(p, "/") {
		if strings.TrimRight(part, ". ") != part || strings.ContainsAny(part, `<>"|?*`) {
			return fmt.Errorf("unsafe skill file path %q", p)
		}
		for _, c := range part {
			if c < 32 || c == 127 {
				return fmt.Errorf("unsafe skill file path %q", p)
			}
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return fmt.Errorf("unsafe skill file path %q", p)
		}
	}
	return nil
}

func validSkillName(name string) error {
	if err := portablePath(name); err != nil {
		return err
	}
	if strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("unsafe skill name path %q", name)
	}
	return nil
}

// Reject links in all mutable path components. macOS's system /var and /tmp
// aliases are allowed; skill directories and their parents are never resolved
// through links on the strength of a receipt or server response.
func noSymlinkPath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return fmt.Errorf("unsafe destination path %q", p)
	}
	for at := p; at != filepath.Dir(at); at = filepath.Dir(at) {
		fi, err := os.Lstat(at)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect path %s: %w", at, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if runtime.GOOS == "darwin" && (at == "/var" || at == "/tmp") {
				continue
			}
			return fmt.Errorf("symlink path %s is not owned by skill installer", at)
		}
	}
	return nil
}

// digestTree includes relative paths, file bytes and executable bits. Refuse
// symlinks and special files instead of hashing their external targets.
func digestTree(dir string) (string, error) {
	return hashTree(dir, false)
}

func hashTree(dir string, links bool) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			fmt.Fprintf(h, "dir:%s\x00", filepath.ToSlash(rel))
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if links && info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "link:%s:%s\x00", filepath.ToSlash(rel), target)
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file %s", p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "file:%s:%o:%d\x00", filepath.ToSlash(rel), info.Mode().Perm()&0111, len(b))
		_, _ = h.Write(b)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copyBackup preserves symlinks themselves, never their targets. Unsupported
// special files fail the operation before an existing package can be removed.
func copyBackup(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0700)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot back up special file %s", p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0600|info.Mode().Perm()&0111)
	})
}

func preserveBackup(dir string, receipt Receipt) error {
	root, err := paths.ActiveRoot()
	if err != nil {
		return err
	}
	base := filepath.Join(root, "backups")
	if err := noSymlinkPath(base); err != nil {
		return err
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return err
	}
	dst, err := os.MkdirTemp(base, "skill-*")
	if err != nil {
		return err
	}
	if err := copyBackup(dir, filepath.Join(dst, "content")); err != nil {
		return fmt.Errorf("backup %s: %w", dir, err)
	}
	meta, err := json.MarshalIndent(struct {
		OriginalPath string  `json:"original_path"`
		Receipt      Receipt `json:"receipt"`
	}{dir, receipt}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "provenance.json"), meta, 0600)
}

func ownedUnmodified(dir string, receipt Receipt) bool {
	digest, err := digestTree(dir)
	if err != nil {
		return false
	}
	for _, e := range receipt.Skills {
		if e.Path == filepath.Join(dir, "SKILL.md") && e.Source != "" && e.Digest == digest {
			return true
		}
	}
	return false
}

// RecoveryDirectory is outside all host discovery roots. Each child contains
// preserved content and its original path/receipt; callers surface it to users.
func RecoveryDirectory() string {
	root, err := paths.ActiveRoot()
	if err != nil {
		return ""
	}
	p := filepath.Join(root, "backups")
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p
	}
	return ""
}

func scopedCatalogContent(dir string, receipt Receipt) bool {
	for _, e := range receipt.Skills {
		if e.Path == filepath.Join(dir, "SKILL.md") && e.Source == "catalog" && (e.Scope == "organization" || e.Scope == "personal") {
			return true
		}
	}
	return false
}

// renameTree is a failure-injection seam at the commit boundary. Production
// always uses rename: activation and rollback never copy into a visible tree.
var renameTree = os.Rename

func unchangedTree(dir string, info fs.FileInfo, digest string) bool {
	current, err := os.Lstat(dir)
	if info == nil {
		return os.IsNotExist(err)
	}
	if err != nil || !os.SameFile(info, current) {
		return false
	}
	now, err := hashTree(dir, true)
	return err == nil && now == digest
}

func applyPackages(writes []packageWrite, retire []Installation, hosts []harness.Harness) (installed []Installation, err error) {
	receiptPath, err := paths.Installed()
	if err != nil {
		return nil, err
	}
	if err := noSymlinkPath(receiptPath); err != nil {
		return nil, err
	}
	before, readErr := os.ReadFile(receiptPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	var receipt Receipt
	if readErr == nil {
		if err := json.Unmarshal(before, &receipt); err != nil {
			return nil, fmt.Errorf("parse receipt: %w", err)
		}
	}
	for _, w := range writes {
		if err := validSkillName(w.name); err != nil {
			return nil, err
		}
	}
	for _, h := range hosts {
		if h.SkillDir == string(filepath.Separator) {
			return nil, fmt.Errorf("unsafe skill root path %q", h.SkillDir)
		}
		if err := noSymlinkPath(h.SkillDir); err != nil {
			return nil, err
		}
		if rel, e := filepath.Rel(h.SkillDir, receiptPath); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("skill path contains receipt: %s", h.SkillDir)
		}
	}
	var changes []*treeChange
	committed := false
	defer func() {
		if !committed {
			for i := len(changes) - 1; i >= 0; i-- {
				c := changes[i]
				if c.activated {
					if noSymlinkPath(c.dest) != nil || !unchangedTree(c.dest, c.activeInfo, c.activeDigest) {
						err = errors.Join(err, fmt.Errorf("rollback ownership changed at %s; recover prior package from %s", c.dest, c.old))
						continue
					}
					// Move only the directory we activated back to its private
					// staging slot; never recursively delete a visible path.
					if e := renameTree(c.dest, c.stage); e != nil {
						err = errors.Join(err, fmt.Errorf("rollback %s: %w (backup %s)", c.dest, e, c.old))
						continue
					}
					c.activated = false
				}
				if c.hadOld {
					if _, e := os.Lstat(c.dest); !os.IsNotExist(e) {
						err = errors.Join(err, fmt.Errorf("rollback destination occupied at %s; recover prior package from %s", c.dest, c.old))
						continue
					}
					if e := renameTree(c.old, c.dest); e != nil {
						err = errors.Join(err, fmt.Errorf("restore %s from %s: %w", c.dest, c.old, e))
						continue
					}
					c.hadOld = false
				}
			}
			installed = nil
		}
		for _, c := range changes {
			// Failed rollback leaves its recoverable workspace intact.
			if committed || (!c.hadOld && !c.activated) {
				_ = os.RemoveAll(c.workspace)
			}
		}
	}()

	seen := make(map[string]*treeChange)
	next := receipt
	next.Skills = append([]Installation(nil), receipt.Skills...)
	now := time.Now().UTC()
	stage := func(dest string, write func(string) error, mustBeAbsent bool) (*treeChange, error) {
		if err := noSymlinkPath(dest); err != nil {
			return nil, err
		}
		parent := filepath.Dir(filepath.Dir(dest)) // beside, never inside, the discovery root
		if err := os.MkdirAll(parent, 0700); err != nil {
			return nil, err
		}
		workspace, err := os.MkdirTemp(parent, ".praxis-stage-*")
		if err != nil {
			return nil, err
		}
		c := &treeChange{dest: dest, workspace: workspace, stage: filepath.Join(workspace, "new"), old: filepath.Join(workspace, "old"), mustBeAbsent: mustBeAbsent}
		changes = append(changes, c)
		if write != nil {
			if err := write(c.stage); err != nil {
				return nil, err
			}
			fi, err := os.Lstat(filepath.Join(c.stage, "SKILL.md"))
			if err != nil || !fi.Mode().IsRegular() {
				return nil, fmt.Errorf("skill path must contain regular SKILL.md: %s", c.stage)
			}
		}
		if fi, err := os.Lstat(dest); err == nil {
			if mustBeAbsent {
				return nil, fmt.Errorf("preserving concurrently created skill: %s", dest)
			}
			c.priorInfo = fi
			c.priorDigest, err = hashTree(dest, true)
			if err != nil {
				return nil, err
			}
			// Archive even unmodified scoped policy when the current catalog
			// replaces/revokes it. Preservation must not mean leaving another
			// organization's instructions in active discovery.
			newDigest, _ := hashTree(c.stage, true)
			archiveScoped := scopedCatalogContent(dest, receipt) && (write == nil || newDigest != c.priorDigest)
			if !ownedUnmodified(dest, receipt) || archiveScoped {
				if err := preserveBackup(dest, receipt); err != nil {
					return nil, err
				}
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		return c, nil
	}
	for _, w := range writes {
		for _, h := range hosts {
			dest := filepath.Join(h.SkillDir, w.name)
			c, ok := seen[dest]
			if !ok {
				c, err = stage(dest, w.write, w.mustBeAbsent)
				if err != nil {
					return nil, err
				}
				seen[dest] = c
			}
			digest, e := digestTree(c.stage)
			if e != nil {
				return nil, e
			}
			entry := Installation{SkillName: w.name, Harness: h.Name, Path: filepath.Join(dest, "SKILL.md"), InstalledAt: now, Source: w.source, Scope: w.scope, Digest: digest}
			installed = append(installed, entry)
			next = upsert(next, entry)
		}
	}
	for _, e := range retire {
		dest := filepath.Dir(e.Path)
		if _, ok := seen[dest]; ok {
			continue
		}
		// A receipt does not grant authority outside the explicitly selected
		// host roots, even if it was copied from another project.
		allowed := false
		for _, h := range hosts {
			if e.Path == filepath.Join(h.SkillDir, e.SkillName, "SKILL.md") && validSkillName(e.SkillName) == nil {
				allowed = true
			}
		}
		if !allowed {
			continue
		}
		if err := noSymlinkPath(dest); err != nil {
			continue
		} // preserve linked legacy packages
		c, e2 := stage(dest, nil, false)
		if e2 != nil {
			return nil, e2
		}
		seen[dest] = c
		kept := next.Skills[:0]
		for _, in := range next.Skills {
			if in.Path != e.Path {
				kept = append(kept, in)
			}
		}
		next.Skills = kept
	}
	for _, c := range changes {
		if err := noSymlinkPath(c.dest); err != nil {
			return nil, err
		}
		if !unchangedTree(c.dest, c.priorInfo, c.priorDigest) {
			return nil, fmt.Errorf("skill tree changed after staging: %s", c.dest)
		}
		if err := os.MkdirAll(filepath.Dir(c.dest), 0700); err != nil {
			return nil, err
		}
		if _, e := os.Lstat(c.dest); e == nil {
			if c.mustBeAbsent {
				return nil, fmt.Errorf("preserving concurrently created skill: %s", c.dest)
			}
			if err := renameTree(c.dest, c.old); err != nil {
				return nil, err
			}
			c.hadOld = true
		} else if !os.IsNotExist(e) {
			return nil, e
		}
		if _, e := os.Stat(c.stage); e == nil {
			c.activeInfo, err = os.Lstat(c.stage)
			if err != nil {
				return nil, err
			}
			c.activeDigest, err = hashTree(c.stage, true)
			if err != nil {
				return nil, err
			}
			activate := renameTree
			if c.mustBeAbsent {
				activate = renameNewTree
			}
			if err := activate(c.stage, c.dest); err != nil {
				return nil, err
			}
			c.activated = true
		}
	}
	current, currentErr := os.ReadFile(receiptPath)
	if (currentErr != nil && !os.IsNotExist(currentErr)) || os.IsNotExist(readErr) != os.IsNotExist(currentErr) || !bytes.Equal(before, current) {
		return nil, fmt.Errorf("receipt changed during skill transaction; existing receipt preserved")
	}
	if err := saveReceipt(next); err != nil {
		return nil, fmt.Errorf("save receipt: %w", err)
	}
	committed = true
	return installed, nil
}
