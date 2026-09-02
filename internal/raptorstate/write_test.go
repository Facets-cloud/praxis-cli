package raptorstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrite_HomeFile(t *testing.T) {
	tests := []struct {
		name         string
		existing     string // "" = no file
		section      string
		cpURL        string
		wantChanged  bool
		wantReplaced string
		wantContains []string
	}{
		{
			name:         "creates the file and section",
			section:      "default",
			cpURL:        "https://cp.test",
			wantChanged:  true,
			wantContains: []string{"[default]\n", "control_plane_url = https://cp.test\n", "username = u@x\n", "token = pat\n"},
		},
		{
			name:         "url is reduced to scheme and host",
			section:      "default",
			cpURL:        "https://cp.test/some/prefix/",
			wantChanged:  true,
			wantContains: []string{"control_plane_url = https://cp.test\n"},
		},
		{
			name:        "identical section is left alone",
			existing:    "[default]\ncontrol_plane_url = https://cp.test\nusername = u@x\ntoken = pat\n",
			section:     "default",
			cpURL:       "https://cp.test",
			wantChanged: false,
		},
		{
			name:         "other sections and extra keys survive",
			existing:     "[other]\ncontrol_plane_url = https://other.test\nusername = o\ntoken = t\ncontrol_plane_project = proj\n",
			section:      "default",
			cpURL:        "https://cp.test",
			wantChanged:  true,
			wantContains: []string{"[other]\n", "control_plane_project = proj\n", "[default]\n"},
		},
		{
			name:         "different host is overwritten and reported",
			existing:     "[default]\ncontrol_plane_url = https://old.test\nusername = u@x\ntoken = old\n",
			section:      "default",
			cpURL:        "https://cp.test",
			wantChanged:  true,
			wantReplaced: "https://old.test",
			wantContains: []string{"token = pat\n"},
		},
		{
			name:         "same host new token is not a replacement",
			existing:     "[default]\ncontrol_plane_url = https://cp.test\nusername = u@x\ntoken = old\n",
			section:      "default",
			cpURL:        "https://cp.test",
			wantChanged:  true,
			wantContains: []string{"token = pat\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".facets", "credentials")
			if tt.existing != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tt.existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			w, err := Write(tt.section, tt.cpURL, "u@x", "pat", "")
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if w.Path != path || w.Section != tt.section {
				t.Errorf("Written = %+v, want path %q section %q", w, path, tt.section)
			}
			if w.Changed != tt.wantChanged || w.Replaced != tt.wantReplaced {
				t.Errorf("Changed=%v Replaced=%q, want %v/%q", w.Changed, w.Replaced, tt.wantChanged, tt.wantReplaced)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(string(data), want) {
					t.Errorf("file missing %q:\n%s", want, data)
				}
			}
			if tt.wantChanged {
				fi, _ := os.Stat(path)
				if fi.Mode().Perm() != 0o600 {
					t.Errorf("mode = %o, want 0600", fi.Mode().Perm())
				}
			}
			// The result must still be a file raptor itself can load: every
			// section carries all three required keys.
			for name, kv := range loadProfiles(path) {
				for _, k := range []string{"control_plane_url", "username", "token"} {
					if kv[k] == "" {
						t.Errorf("section %q lost required key %q", name, k)
					}
				}
			}
		})
	}
}

func TestWrite_LocalDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()

	w, err := Write("acme", "https://acme.test", "u@x", "pat", repo)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := filepath.Join(repo, ".facets", "credentials")
	if w.Path != want || !w.Changed {
		t.Errorf("Written = %+v, want path %q changed", w, want)
	}
	gi, err := os.ReadFile(filepath.Join(repo, ".facets", ".gitignore"))
	if err != nil || string(gi) != gitignoreBody {
		t.Errorf(".gitignore = %q, %v; want %q", gi, err, gitignoreBody)
	}
	// The home file is never touched by a local write.
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".facets", "credentials")); !os.IsNotExist(err) {
		t.Errorf("home credentials should not exist, stat err = %v", err)
	}

	// An existing .gitignore is the user's; leave it as it is.
	custom := filepath.Join(repo, ".facets", ".gitignore")
	if err := os.WriteFile(custom, []byte("# mine\ncredentials\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write("acme", "https://acme.test", "u@x", "pat2", repo); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(custom); string(got) != "# mine\ncredentials\n" {
		t.Errorf(".gitignore overwritten: %q", got)
	}
}

func TestWrite_Rejects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tests := []struct {
		name                           string
		section, cpURL, user, tok, dir string
	}{
		{"empty name", "", "https://cp.test", "u", "t", ""},
		{"empty url", "default", "", "u", "t", ""},
		{"empty username", "default", "https://cp.test", "", "t", ""},
		{"empty token", "default", "https://cp.test", "u", "", ""},
		{"url without scheme", "default", "cp.test", "u", "t", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Write(tt.section, tt.cpURL, tt.user, tt.tok, tt.dir); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestResolve_LocalFileShadowsHome(t *testing.T) {
	// raptor reads the first .facets/credentials walking up from cwd and
	// ignores the home file when one is found; praxis must see the same file.
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearRaptorEnv(t)
	stubInstalled(t, true)
	homeCreds := filepath.Join(home, ".facets", "credentials")
	if err := os.MkdirAll(filepath.Dir(homeCreds), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homeCreds, []byte("[default]\ncontrol_plane_url = https://home.test\nusername = h\ntoken = ht\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if _, err := Write("acme", "https://acme.test", "u@x", "pat", repo); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "deep", "er")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, cwd, pin string
		want           State
	}{
		{
			name: "outside the repo the home file resolves",
			cwd:  home,
			want: State{Installed: true, Found: true, Profile: "default", Source: SourceDefault, ControlPlaneURL: "https://home.test", Username: "h"},
		},
		{
			name: "inside the repo the local sole profile resolves",
			cwd:  sub,
			want: State{Installed: true, Found: true, Profile: "acme", Source: SourceSole, ControlPlaneURL: "https://acme.test", Username: "u@x"},
		},
		{
			name: "inside the repo the pin finds the local section",
			cwd:  sub, pin: "acme",
			want: State{Installed: true, Found: true, Pinned: true, Profile: "acme", Source: SourcePin, ControlPlaneURL: "https://acme.test", Username: "u@x"},
		},
		{
			name: "inside the repo the home default is invisible",
			cwd:  sub, pin: "default",
			want: State{Installed: true, Pinned: true, Profile: "default", Source: SourcePin},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(SetGetwdForTest(func() (string, error) { return tt.cwd, nil }))
			if got := Resolve(tt.pin); got != tt.want {
				t.Errorf("Resolve(%q) from %s = %+v, want %+v", tt.pin, tt.cwd, got, tt.want)
			}
			if _, _, ok := PAT("acme"); ok != (tt.cwd != home) {
				t.Errorf("PAT(acme) ok = %v from %s", ok, tt.cwd)
			}
		})
	}
}

// TestMain starts the raptor-file walk at the faked HOME so no test reads the
// developer's live ~/.facets/credentials (see SetGetwdForTest).
func TestMain(m *testing.M) {
	restore := SetGetwdForTest(func() (string, error) { return os.Getenv("HOME"), nil })
	code := m.Run()
	restore()
	os.Exit(code)
}

func TestWrite_IOFailures(t *testing.T) {
	t.Run("no home directory for a global write", func(t *testing.T) {
		t.Setenv("HOME", "")
		if _, err := Write("default", "https://cp.test", "u", "t", ""); err == nil {
			t.Error("want error when HOME cannot be resolved")
		}
	})
	t.Run(".facets is a file", func(t *testing.T) {
		repo := t.TempDir()
		if err := os.WriteFile(filepath.Join(repo, ".facets"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Write("default", "https://cp.test", "u", "t", repo); err == nil {
			t.Error("want error when .facets cannot be created")
		}
	})
	t.Run(".facets is read-only", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root ignores directory modes")
		}
		repo := t.TempDir()
		dir := filepath.Join(repo, ".facets")
		if err := os.Mkdir(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		if _, err := Write("default", "https://cp.test", "u", "t", repo); err == nil {
			t.Error("want error when the temp file cannot be created")
		}
	})
}
