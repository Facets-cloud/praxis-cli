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
	got := make(chan struct{})
	go func() {
		second, err := lockFile(open())
		if err == nil {
			second()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("the second lock did not wait for the first")
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the second lock did not get the lock after the release")
	}
}
