package agentinstall

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

func TestReceiptWritersWaitBeforeReadingSharedReceipt(t *testing.T) {
	for _, op := range []string{"agent-install", "agent-remove", "skill-remove", "skill-prefix-remove"} {
		t.Run(op, func(t *testing.T) {
			home := setupHome(t)
			hosts := fakeHarnesses(t, home)
			root := filepath.Join(home, ".praxis")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(filepath.Join(root, ".skills.lock"), os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
				t.Fatal(err)
			}
			defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			done := make(chan error, 1)
			go func() {
				var e error
				switch op {
				case "agent-install":
					_, e = Install([]agentcatalog.Agent{{Name: "alpha", Description: "test", SystemPrompt: "test", IsActive: true, Kind: agentcatalog.KindAgent}}, hosts)
				case "agent-remove":
					_, e = UninstallByPrefix("praxis-")
				case "skill-remove":
					_, e = skillinstall.Uninstall("missing")
				case "skill-prefix-remove":
					_, e = skillinstall.UninstallByPrefix("missing-")
				}
				done <- e
			}()
			finished := false
			select {
			case err := <-done:
				finished = true
				t.Errorf("%s did not wait for shared receipt lock: %v", op, err)
			case <-time.After(100 * time.Millisecond):
			}
			// Simulate the already-lock-holding skill transaction committing R1.
			if err := saveReceipt(skillinstall.Receipt{Skills: []skillinstall.Installation{{SkillName: "sentinel", Source: "embedded", Digest: "R1"}}}); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
				t.Fatal(err)
			}
			if !finished {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("writer did not resume")
				}
			}
			r, err := loadReceipt()
			if err != nil || len(r.Skills) != 1 || r.Skills[0].Digest != "R1" {
				t.Fatalf("concurrent committed skill metadata lost: %+v %v", r, err)
			}
		})
	}
}
