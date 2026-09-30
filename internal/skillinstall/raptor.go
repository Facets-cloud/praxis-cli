package skillinstall

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
)

// InstallRaptor installs only missing packages. Existing local sources remain
// independently owned; invalid/shadowing copies need explicit repair, not overwrite.
func InstallRaptor(binary string, hosts []harness.Harness) (installed []Installation, err error) {
	if len(hosts) == 0 {
		return nil, nil
	}
	err = withSkillLock(func() error {
		missing, e := missingRaptorHosts(hosts)
		if e != nil {
			return e
		}
		if len(missing) == 0 {
			return nil
		}
		if binary == "" {
			return fmt.Errorf("Raptor CLI missing; run praxis login to install it and its skill")
		}
		stage, e := os.MkdirTemp("", "praxis-raptor-export-*")
		if e != nil {
			return e
		}
		defer os.RemoveAll(stage)
		// Raptor walks cwd's parents before HOME; fence it even under nested TMPDIR.
		if e := os.Mkdir(filepath.Join(stage, ".facets"), 0700); e != nil {
			return e
		}
		if e := os.WriteFile(filepath.Join(stage, ".facets", "credentials"), nil, 0600); e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "install-skills", "--agent", "claude", "--path", stage)
		cmd.Dir = stage
		for _, env := range os.Environ() {
			if strings.HasPrefix(env, "HOME=") || strings.HasPrefix(env, "FACETS_") || strings.HasPrefix(env, "PRAXIS_") || strings.HasPrefix(env, "RAPTOR_") {
				continue
			}
			cmd.Env = append(cmd.Env, env)
		}
		cmd.Env = append(cmd.Env, "HOME="+stage, "RAPTOR_NO_SKILLS_UPGRADE=1")
		if e := cmd.Run(); e != nil {
			return fmt.Errorf("Raptor skill export failed: %w; check raptor install-skills or upgrade Raptor", e)
		}
		source := filepath.Join(stage, ".claude", "skills", "raptor")
		if e := verifyPackage(source, "raptor"); e != nil {
			return fmt.Errorf("Raptor binary did not export a complete consolidated skill: %w; upgrade Raptor", e)
		}
		current, e := missingRaptorHosts(hosts)
		if e != nil {
			return e
		}
		if len(current) != len(missing) {
			return fmt.Errorf("Raptor skill appeared during export; left in place, retry installation")
		}
		for i, h := range current {
			if h.SkillDir != missing[i].SkillDir {
				return fmt.Errorf("Raptor skill discovery changed during export; retry installation")
			}
		}
		w := packageWrite{name: "raptor", source: "raptor", scope: "builtin", mustBeAbsent: true, write: func(dir string) error { return writeTree(os.DirFS(source), dir) }}
		installed, e = applyPackages([]packageWrite{w}, nil, missing)
		return e
	})
	return
}

func missingRaptorHosts(hosts []harness.Harness) ([]harness.Harness, error) {
	var missing []harness.Harness
	for _, h := range hosts {
		if verifiedRaptor(h) {
			continue
		}
		for _, root := range raptorRoots(h) {
			if _, err := os.Lstat(filepath.Join(root, "raptor")); !os.IsNotExist(err) {
				return nil, fmt.Errorf("preserving invalid Raptor skill at %s; repair its source explicitly", root)
			}
		}
		// Both hosts read .agents/skills, even when only one was selected for
		// setup. Avoid introducing a duplicate for the peer's native package.
		if native := nativeRaptorRoot(h); native != h.SkillDir {
			peer := h
			if h.Name == "codex" {
				peer.Name = "gemini-cli"
			} else {
				peer.Name = "codex"
			}
			for _, root := range raptorRoots(peer) {
				if filepath.Base(filepath.Dir(root)) == ".agents" {
					continue
				}
				if _, err := os.Lstat(filepath.Join(root, "raptor")); !os.IsNotExist(err) {
					h.SkillDir = native
					break
				}
			}
		}
		missing = append(missing, h)
	}
	return missing, nil
}

func nativeRaptorRoot(h harness.Harness) string {
	if filepath.Base(h.SkillDir) != "skills" || filepath.Base(filepath.Dir(h.SkillDir)) != ".agents" {
		return h.SkillDir
	}
	var native string
	switch h.Name {
	case "codex":
		native = ".codex"
	case "gemini-cli":
		native = ".gemini"
	default:
		return h.SkillDir
	}
	return filepath.Join(filepath.Dir(filepath.Dir(h.SkillDir)), native, "skills")
}
