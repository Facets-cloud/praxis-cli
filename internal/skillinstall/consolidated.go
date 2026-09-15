package skillinstall

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
	"go.yaml.in/yaml/v3"
)

var canonicalTree = func() fs.FS { tree, _ := treeSkillFS("praxis"); return tree }

func embeddedWrite(name string) (packageWrite, error) {
	var tree fs.FS
	if name == "praxis" {
		tree = canonicalTree()
	} else {
		tree, _ = treeSkillFS(name)
	}
	if tree != nil {
		return packageWrite{name: name, source: "embedded", scope: "builtin", write: func(dir string) error { return writeTree(tree, dir) }}, nil
	}
	body, err := ContentFor(name)
	if err != nil {
		return packageWrite{}, err
	}
	return packageWrite{name: name, source: "embedded", scope: "builtin", write: func(dir string) error { return writeBodies(dir, body, nil) }}, nil
}

// InstallCatalog validates and installs the entire bundle as one transaction.
// Scope is retained in the receipt; a prefixed name alone conveys no ownership.
func InstallCatalog(skills []skillcatalog.Skill, hosts []harness.Harness) (out []Installation, err error) {
	err = withSkillLock(func() error {
		writes, e := catalogWrites(skills)
		if e != nil {
			return e
		}
		out, e = applyPackages(writes, nil, hosts)
		return e
	})
	return
}

func catalogWrites(skills []skillcatalog.Skill) ([]packageWrite, error) {
	var out []packageWrite
	seen := map[string]int{}
	rank := map[string]int{"global": 1, "organization": 2, "personal": 3}
	for _, sk := range skills {
		if err := validSkillName(sk.Name); err != nil {
			return nil, err
		}
		name := sk.PrefixedName()
		// Former prefixed built-ins are no longer reserved. Organization and
		// personal namesakes may use those names; their receipt scope protects
		// them from retirement. The canonical bare "praxis" cannot collide
		// with a catalog name because every catalog path is prefixed.
		files := make([]FileBody, len(sk.Files))
		for i, f := range sk.Files {
			files[i] = FileBody{Path: f.Path, Content: f.Content}
		}
		body := sk.RenderedContent()
		w := packageWrite{name: name, source: "catalog", scope: sk.Scope, write: func(dir string) error { return writeBodies(dir, body, files) }}
		key := strings.ToLower(name)
		if i, ok := seen[key]; ok {
			if out[i].name != name || out[i].scope == sk.Scope {
				return nil, fmt.Errorf("duplicate catalog skill path %q", name)
			}
			if rank[sk.Scope] > rank[out[i].scope] {
				out[i] = w
			}
			continue
		}
		seen[key] = len(out)
		out = append(out, w)
	}
	return out, nil
}

type SyncResult struct {
	Canonical, Installed, Removed []Installation
	Capabilities                  []string
	Err                           error
}

// SyncCatalog serializes installation, verification, negotiation and catalog
// activation. Offline canonical installation is independent of catalog fetch.
// A failed catalog fetch or commit leaves its last usable files and receipt.
func SyncCatalog(hosts []harness.Harness, fetch func([]string) ([]skillcatalog.Skill, error)) (result SyncResult) {
	if len(hosts) == 0 {
		return result
	}
	result.Err = withSkillLock(func() error {
		w, installErr := embeddedWrite("praxis")
		if installErr == nil {
			result.Canonical, installErr = applyPackages([]packageWrite{w}, nil, hosts)
		}
		caps := VerifiedCapabilities(hosts)
		if installErr != nil {
			caps = withoutCapability(caps, "praxis-v1")
		}
		result.Capabilities = caps
		skills, fetchErr := fetch(append([]string(nil), caps...))
		if fetchErr != nil {
			return errors.Join(installErr, fmt.Errorf("catalog fetch failed: %w; existing skills left in place", fetchErr))
		}
		// Re-check after a slow fetch: external tools may have moved Raptor.
		// If a negotiated replacement disappeared, abort rather than consuming
		// an already filtered response and leaving a hole in the skill set.
		current := VerifiedCapabilities(hosts)
		for _, c := range caps {
			if !hasCapability(current, c) {
				return errors.Join(installErr, fmt.Errorf("replacement %s changed during catalog fetch; existing skills left in place", c))
			}
		}
		writes, err := catalogWrites(skillcatalog.FilterConsolidated(skills, caps))
		if err != nil {
			return errors.Join(installErr, err)
		}
		receipt, err := loadReceipt()
		if err != nil {
			return errors.Join(installErr, err)
		}
		// The successful complete bundle is authoritative for this profile.
		// Namesakes still in that bundle survive catalogWrites' scope ranking;
		// a previous org's policy must not shadow the current catalog forever.
		present := map[string]bool{}
		for _, w := range writes {
			present[w.name] = true
		}
		retire := retirements(receipt, hosts, caps)
		retired := map[string]bool{}
		for _, e := range retire {
			retired[e.Path] = true
		}
		for _, e := range receipt.Skills {
			managed := e.Source == "catalog" && (e.Scope == "global" || e.Scope == "organization" || e.Scope == "personal")
			// A pre-provenance receipt establishes managed installation, but
			// not scope/unchanged content. Archive absent prefixed entries;
			// never treat them as verified globals or delete untracked files.
			legacyTracked := e.Source == "" && e.Scope == "" && strings.HasPrefix(e.SkillName, skillcatalog.PraxisPrefix) && (!knownBuiltin(e) || hasCapability(caps, "praxis-v1"))
			if (managed || legacyTracked) && entryOnHosts(e, hosts) && !present[e.SkillName] && !retired[e.Path] && noSymlinkPath(filepath.Dir(e.Path)) == nil {
				retire = append(retire, e)
				retired[e.Path] = true
			}
		}
		result.Installed, err = applyPackages(writes, retire, hosts)
		if err == nil {
			result.Removed = retire
		}
		return errors.Join(installErr, err)
	})
	return result
}

func hasCapability(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}
func withoutCapability(caps []string, excluded string) []string {
	var out []string
	for _, c := range caps {
		if c != excluded {
			out = append(out, c)
		}
	}
	return out
}
func entryOnHosts(e Installation, hosts []harness.Harness) bool {
	if validSkillName(e.SkillName) != nil {
		return false
	}
	for _, h := range hosts {
		if e.Path == filepath.Join(h.SkillDir, e.SkillName, "SKILL.md") {
			return true
		}
	}
	return false
}

func retirements(receipt Receipt, hosts []harness.Harness, caps []string) []Installation {
	var out []Installation
	for _, e := range receipt.Skills {
		if !entryOnHosts(e, hosts) || noSymlinkPath(filepath.Dir(e.Path)) != nil {
			continue
		}
		if e.Scope == "organization" || e.Scope == "personal" {
			continue
		}
		legacyBuiltin := e.SkillName != "praxis" && IsMetaSkill(e.SkillName) && knownBuiltin(e) && hasCapability(caps, "praxis-v1")
		global := strings.HasPrefix(e.SkillName, skillcatalog.PraxisPrefix) && skillcatalog.ReplacedGlobal(strings.TrimPrefix(e.SkillName, skillcatalog.PraxisPrefix), e.Scope, caps)
		if legacyBuiltin || global {
			out = append(out, e)
		}
	}
	return out
}

// Pre-provenance receipts are recognized only by the retained built-in's exact
// entrypoint bytes. Unknown/modified namesakes stay in place; recognized old
// content without a digest is backed up before retirement.
func knownBuiltin(e Installation) bool {
	if e.Source == "embedded" {
		return true
	}
	if e.Source != "" || e.Scope != "" {
		return false
	}
	body, err := ContentFor(e.SkillName)
	if err != nil {
		return false
	}
	actual, err := os.ReadFile(e.Path)
	return err == nil && bytes.Equal(actual, []byte(body))
}

// VerifiedCapabilities is deliberately local and fail-closed. Praxis needs
// this installer's complete-tree receipt; Raptor is independently installed,
// so it needs a canonical loadable root and a complete local reference graph.
// Executables, old prefixed skills and a directory name alone prove nothing.
func VerifiedCapabilities(hosts []harness.Harness) []string {
	if len(hosts) == 0 {
		return nil
	}
	r, err := loadReceipt()
	if err != nil {
		return nil
	}
	praxis, raptor := true, true
	for _, h := range hosts {
		p := filepath.Join(h.SkillDir, "praxis")
		if noSymlinkPath(p) != nil || verifyPackage(p, "praxis") != nil {
			praxis = false
		} else {
			digest, err := digestTree(p)
			found := false
			if err == nil {
				for _, e := range r.Skills {
					if e.Source == "embedded" && e.SkillName == "praxis" && e.Harness == h.Name && e.Path == filepath.Join(p, "SKILL.md") && e.Digest == digest {
						found = true
					}
				}
			}
			praxis = praxis && found
		}
		raptor = raptor && verifiedRaptor(h)
	}
	var caps []string
	if praxis {
		caps = append(caps, "praxis-v1")
	}
	if raptor {
		caps = append(caps, "raptor-v1")
	}
	return caps
}

func verifiedRaptor(h harness.Harness) bool {
	found := false
	for _, root := range raptorRoots(h) {
		dir := filepath.Join(root, "raptor")
		if _, err := os.Lstat(dir); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return false
		}
		// A broken project/native copy can shadow an otherwise healthy user
		// package. Do not claim a working loader by selecting the good copy.
		if verifyPackage(dir, "raptor") != nil {
			return false
		}
		found = true
	}
	return found
}

func raptorRoots(h harness.Harness) []string {
	// Native and shared user roots can be inherited by a project-scoped host.
	roots := []string{h.SkillDir}
	if native := nativeRaptorRoot(h); native != h.SkillDir {
		roots = append(roots, native)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return roots
	}
	var native []string
	switch h.Name {
	case "claude-code":
		native = []string{".claude/skills"}
	case "codex":
		native = []string{".agents/skills", ".codex/skills"}
	case "gemini-cli":
		native = []string{".agents/skills", ".gemini/skills"}
	case "antigravity":
		native = []string{".gemini/config/skills"}
	}
	for _, p := range native {
		roots = append(roots, filepath.Join(home, filepath.FromSlash(p)))
	}
	return roots
}

var markdownLink = regexp.MustCompile(`\[[^\]]*\]\(<?([^\s)>]+)>?(?:\s+[^)]*)?\)`)
var referenceLink = regexp.MustCompile(`(?m)^\s*\[[^\]]+\]:\s*<?([^\s>]+)>?`)
var quotedLocalPath = regexp.MustCompile("`((?:references|refs|scripts|assets|flows)/[^` \\n]+)`")

func verifyPackage(dir, name string) error {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("package root is not a directory")
	}
	if _, err := digestTree(root); err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		return err
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return fmt.Errorf("missing frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return fmt.Errorf("invalid frontmatter")
	}
	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(text[4:4+end]), &frontmatter); err != nil {
		return fmt.Errorf("invalid frontmatter YAML: %w", err)
	}
	actualName, nameOK := frontmatter["name"].(string)
	description, descriptionOK := frontmatter["description"].(string)
	metadata, metadataOK := frontmatter["metadata"].(map[string]any)
	version, versionOK := metadata["version"].(string)
	if !nameOK || !descriptionOK || !metadataOK || !versionOK || actualName != name || version != "1.0" || strings.TrimSpace(description) == "" {
		return fmt.Errorf("not canonical %s v1.0", name)
	}
	seen := map[string]bool{}
	localCount := 0
	var visit func(string) error
	visit = func(p string) error {
		if seen[p] {
			return nil
		}
		seen[p] = true
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		var targets []string
		for _, re := range []*regexp.Regexp{markdownLink, referenceLink, quotedLocalPath} {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				targets = append(targets, m[1])
			}
		}
		for _, target := range targets {
			if strings.HasPrefix(target, "#") {
				continue
			}
			u, err := url.Parse(target)
			if err != nil {
				return err
			}
			if u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto" {
				continue
			}
			if u.Scheme != "" || u.Host != "" || strings.HasPrefix(u.Path, "/") {
				return fmt.Errorf("nonlocal package reference %q", target)
			}
			rel := path.Clean(path.Join(path.Dir(p), u.Path))
			if err := portablePath(rel); err != nil {
				return err
			}
			fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return fmt.Errorf("missing reference %s: %w", rel, err)
			}
			if !fi.Mode().IsRegular() {
				return fmt.Errorf("reference %s is not a file", rel)
			}
			localCount++
			if strings.HasSuffix(strings.ToLower(rel), ".md") {
				if err := visit(rel); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit("SKILL.md"); err != nil {
		return err
	}
	if localCount == 0 {
		return fmt.Errorf("canonical package has no local references")
	}
	return nil
}
