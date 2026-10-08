package skillinstall

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A second lockFile on the same file waits until the first is released. It
// runs on every platform, so it also covers the Windows LockFileEx path.
func TestLockFileIsExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".skills.lock")
	open := func() *os.File {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	unlock, err := lockFile(open())
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		second, err := lockFile(open())
		if err == nil {
			second()
		}
		got <- err
	}()
	select {
	case <-got:
		t.Fatal("the second lock did not wait for the first")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("second lock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second lock did not get the lock after the release")
	}
}

// A digest counts executable bits only where files have them.
func TestExecBitsOn(t *testing.T) {
	if got := execBitsOn("linux", 0o755); got != 0o111 {
		t.Errorf("linux: %o, want 111", got)
	}
	if got := execBitsOn("windows", 0o755); got != 0 {
		t.Errorf("windows: %o, want 0", got)
	}
}
