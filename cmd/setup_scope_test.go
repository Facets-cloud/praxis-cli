package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapFollowsActiveProjectScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	mustMkdir(t, filepath.Join(home, ".claude"))
	project := pinProjectRoot(t, home)
	n, err := installBootstrapSkills(io.Discard, true)
	if err != nil || n != 1 {
		t.Fatalf("bootstrap=%d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude/skills/praxis/SKILL.md")); err != nil {
		t.Errorf("missing project package: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude/skills/praxis")); !os.IsNotExist(err) {
		t.Errorf("project bootstrap changed user skills: %v", err)
	}
	marker, err := bootstrapMarkerPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(marker) != filepath.Join(project, ".praxis") {
		t.Errorf("bootstrap marker crosses scope: %s", marker)
	}
}

func TestSetupReportsRecoverableModifiedContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	dir := filepath.Join(home, ".claude/skills/praxis")
	mustMkdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("edited original"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	oldJSON := setupJSON
	setupJSON = true
	t.Cleanup(func() { setupJSON = oldJSON; setupCmd.SetOut(nil) })
	setupCmd.SetOut(&out)
	if err := setupCmd.RunE(setupCmd, nil); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Recovery string `json:"skill_recovery_directory"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Recovery == "" {
		t.Fatalf("setup omitted recovery directory: %s", out.String())
	}
	found := false
	if err := filepath.WalkDir(result.Recovery, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			found = found || string(b) == "edited original"
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Errorf("reported recovery directory %s does not contain the preserved content", result.Recovery)
	}
}
