package raptorstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeedProfile_CreatesFile covers the case the fix exists for: the user
// created a control-plane PAT at praxis's prompt on a machine where raptor
// has never run.
func TestSeedProfile_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".facets", "credentials")

	wrote, err := seedProfile(path, "default", "https://cp.test", "me@corp.test", "tok-123")
	if err != nil || !wrote {
		t.Fatalf("seedProfile = (%v, %v), want (true, nil)", wrote, err)
	}

	got := loadProfiles(path)["default"]
	for key, want := range map[string]string{
		"control_plane_url": "https://cp.test",
		"username":          "me@corp.test",
		"token":             "tok-123",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}

	// The file holds a control-plane token; it must not be world-readable.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// TestSeedProfile_NeverOverwrites is the safety property: an existing raptor
// credential may point at a different control plane, and praxis has no
// standing to replace it.
func TestSeedProfile_NeverOverwrites(t *testing.T) {
	body := "[default]\ncontrol_plane_url = https://other.test\nusername = them@corp.test\ntoken = keep-me\n"
	path := writeCreds(t, body)

	wrote, err := seedProfile(path, "default", "https://cp.test", "me@corp.test", "tok-123")
	if err != nil {
		t.Fatal(err)
	}
	if wrote {
		t.Error("wrote = true, want false — an existing section must be left alone")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Errorf("file changed:\n got %q\nwant %q", after, body)
	}
}

// TestSeedProfile_AppendsBesideOthers checks the partial case — raptor has
// profiles, just not this one — including a file with no trailing newline,
// where a naive append would fuse the last line into the new header.
func TestSeedProfile_AppendsBesideOthers(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "trailing newline", body: "[staging]\ncontrol_plane_url = https://stg.test\nusername = a@b.c\ntoken = t1\n"},
		{name: "no trailing newline", body: "[staging]\ncontrol_plane_url = https://stg.test\nusername = a@b.c\ntoken = t1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeCreds(t, tc.body)

			wrote, err := seedProfile(path, "default", "https://cp.test", "me@corp.test", "tok-123")
			if err != nil || !wrote {
				t.Fatalf("seedProfile = (%v, %v), want (true, nil)", wrote, err)
			}

			profiles := loadProfiles(path)
			if len(profiles) != 2 {
				t.Fatalf("profiles = %v, want both staging and default", profiles)
			}
			if got := profiles["staging"]["token"]; got != "t1" {
				t.Errorf("staging token = %q, want it untouched (%q)", got, "t1")
			}
			if got := profiles["default"]["control_plane_url"]; got != "https://cp.test" {
				t.Errorf("default control_plane_url = %q, want https://cp.test", got)
			}
		})
	}
}

// TestSeedProfile_IncompleteInput asserts a half-filled credential never
// reaches raptor's store — raptor errors on a section missing any of the
// three required keys, so writing one would be worse than writing nothing.
func TestSeedProfile_IncompleteInput(t *testing.T) {
	tests := []struct {
		name                          string
		profile, url, username, token string
	}{
		{name: "no profile name", url: "https://cp.test", username: "me@corp.test", token: "tok"},
		{name: "no token", profile: "default", url: "https://cp.test", username: "me@corp.test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".facets", "credentials")

			wrote, err := seedProfile(path, tc.profile, tc.url, tc.username, tc.token)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if wrote {
				t.Error("wrote = true, want false")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Error("credentials file was created from incomplete input")
			}
		})
	}
}

// TestSeedProfile_UnreadableFile asserts an unreadable store surfaces as an
// error rather than being mistaken for "absent" and overwritten.
func TestSeedProfile_UnreadableFile(t *testing.T) {
	path := writeCreds(t, "[default]\ntoken = t1\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Skipf("cannot drop read permission here: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	if _, err := seedProfile(path, "acme", "https://cp.test", "me@corp.test", "tok"); err == nil {
		t.Error("err = nil, want a read error rather than a silent overwrite")
	}
}

// TestSeedTarget pins the section name to raptor's own read order. Writing
// [default] while a FACETS_PROFILE shell reads [acme] would leave raptor
// broken by exactly the amount this feature claims to fix.
func TestSeedTarget(t *testing.T) {
	tests := []struct {
		name, pin, envProfile, want string
	}{
		{name: "nothing set", want: "default"},
		{name: "FACETS_PROFILE beats default", envProfile: "acme", want: "acme"},
		{name: "pin beats FACETS_PROFILE", pin: "pinned", envProfile: "acme", want: "pinned"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearRaptorEnv(t)
			if tc.envProfile != "" {
				t.Setenv("FACETS_PROFILE", tc.envProfile)
			}
			if got := seedTarget(tc.pin); got != tc.want {
				t.Errorf("seedTarget(%q) with FACETS_PROFILE=%q = %q, want %q",
					tc.pin, tc.envProfile, got, tc.want)
			}
		})
	}
}

// TestSeedProfile_AtomicWrite: a partial write would truncate raptor's OTHER
// profiles — the loss the never-overwrite rule exists to prevent. Assert the
// temp file is not left behind alongside the credentials.
func TestSeedProfile_AtomicWrite(t *testing.T) {
	path := writeCreds(t, "[staging]\ncontrol_plane_url = https://stg.test\nusername = a@b.c\ntoken = t1\n")

	if _, err := seedProfile(path, "default", "https://cp.test", "me@corp.test", "tok"); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left a temp file behind: %s", e.Name())
		}
	}
}

// TestSeedProfile_UnwritableLocation covers the failure branches of the atomic
// write. Both must surface as an error: a silent failure here means the login
// prints "raptor is logged in" over a file that was never written.
func TestSeedProfile_UnwritableLocation(t *testing.T) {
	t.Run("parent is a file", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "notadir")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := seedProfile(filepath.Join(blocker, "credentials"),
			"default", "https://cp.test", "me@corp.test", "tok")
		if err == nil {
			t.Error("err = nil, want a MkdirAll failure")
		}
	})

	t.Run("directory not writable", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Skipf("cannot drop write permission here: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		_, err := seedProfile(filepath.Join(dir, "credentials"),
			"default", "https://cp.test", "me@corp.test", "tok")
		if err == nil {
			t.Error("err = nil, want a temp-file creation failure")
		}
	})
}

// TestSeedProfile_ExportedWrapper covers SeedProfile itself — DefaultPath plus
// the seedTarget resolution the callers rely on.
func TestSeedProfile_ExportedWrapper(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearRaptorEnv(t)

	wrote, err := SeedProfile("", "https://cp.test", "me@corp.test", "tok-123")
	if err != nil || !wrote {
		t.Fatalf("SeedProfile = (%v, %v), want (true, nil)", wrote, err)
	}

	got := loadProfiles(filepath.Join(home, ".facets", "credentials"))["default"]
	if got["control_plane_url"] != "https://cp.test" {
		t.Errorf("control_plane_url = %q, want https://cp.test", got["control_plane_url"])
	}

	// Second call is the never-overwrite path through the exported wrapper.
	wrote, err = SeedProfile("", "https://other.test", "me@corp.test", "tok-999")
	if err != nil || wrote {
		t.Errorf("second SeedProfile = (%v, %v), want (false, nil)", wrote, err)
	}
}
