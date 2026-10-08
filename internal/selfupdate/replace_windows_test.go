package selfupdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A running .exe cannot be overwritten on Windows; replaceFile renames it aside
// and CleanupOld removes the old copy once nothing runs it.
func TestReplaceFileReplacesARunningExe(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "prog.exe")
	src, err := exec.LookPath("ping.exe")
	if err != nil {
		t.Skip("ping.exe not found: no long-running Windows binary to hold open")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cur, b, 0o755); err != nil {
		t.Fatal(err)
	}
	run := exec.Command(cur, "-n", "30", "127.0.0.1")
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, "new.exe")
	if err := os.WriteFile(newPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceFile(newPath, cur); err != nil {
		t.Fatalf("replaceFile while the old binary runs: %v", err)
	}
	if got, _ := os.ReadFile(cur); string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
	olds, _ := filepath.Glob(cur + ".old-*")
	if len(olds) != 1 {
		t.Fatalf("old copies = %v, want one", olds)
	}

	_ = run.Process.Kill()
	_ = run.Wait()
	CleanupOld(cur)
	if olds, _ := filepath.Glob(cur + ".old-*"); len(olds) != 0 {
		t.Errorf("old copies after cleanup = %v, want none", olds)
	}
}
