package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withIsolatedHome redirects $HOME to a temp dir and clears $PATH, so
// detection has a deterministic empty baseline regardless of what's
// installed on the developer's actual machine.
func withIsolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	return home
}

func TestAll_ReturnsThreeHarnesses(t *testing.T) {
	withIsolatedHome(t)
	got := All()
	if len(got) != 3 {
		t.Errorf("len(All()) = %d, want 3", len(got))
	}
	wantNames := map[string]bool{"claude-code": true, "codex": true, "gemini-cli": true}
	for _, h := range got {
		if !wantNames[h.Name] {
			t.Errorf("unexpected harness %q in All()", h.Name)
		}
		delete(wantNames, h.Name)
	}
	if len(wantNames) > 0 {
		for n := range wantNames {
			t.Errorf("All() missing harness %q", n)
		}
	}
}

func TestAll_NoCursor(t *testing.T) {
	withIsolatedHome(t)
	for _, h := range All() {
		if strings.Contains(strings.ToLower(h.Name), "cursor") {
			t.Errorf("Cursor must not be in All() for v0.1 (no user-scope skill dir)")
		}
	}
}

func TestDetected_NoneByDefault(t *testing.T) {
	withIsolatedHome(t)
	if got := Detected(); len(got) != 0 {
		t.Errorf("Detected() = %d harnesses on isolated machine, want 0", len(got))
	}
}

func TestDetect_ClaudeCode_HomeDir(t *testing.T) {
	home := withIsolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	h, ok := ByName("claude-code")
	if !ok {
		t.Fatal("claude-code missing")
	}
	if !h.Detected {
		t.Errorf("not detected when ~/.claude exists: %+v", h)
	}
}

func TestDetect_Codex_AgentsDir(t *testing.T) {
	home := withIsolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	h, _ := ByName("codex")
	if !h.Detected {
		t.Errorf("Codex not detected when ~/.agents exists: %+v", h)
	}
}

func TestDetect_Codex_CodexDir(t *testing.T) {
	home := withIsolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	h, _ := ByName("codex")
	if !h.Detected {
		t.Errorf("Codex not detected when ~/.codex exists: %+v", h)
	}
}

func TestDetect_GeminiCLI_HomeDir(t *testing.T) {
	home := withIsolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0700); err != nil {
		t.Fatal(err)
	}
	h, _ := ByName("gemini-cli")
	if !h.Detected {
		t.Errorf("Gemini CLI not detected when ~/.gemini exists: %+v", h)
	}
}

func TestSkillDir_PerHarness(t *testing.T) {
	home := withIsolatedHome(t)
	tests := []struct {
		name string
		want string
	}{
		{"claude-code", filepath.Join(home, ".claude", "skills")},
		{"codex", filepath.Join(home, ".agents", "skills")},
		{"gemini-cli", filepath.Join(home, ".gemini", "skills")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ok := ByName(tt.name)
			if !ok {
				t.Fatalf("ByName(%q) = !ok", tt.name)
			}
			if h.SkillDir != tt.want {
				t.Errorf("SkillDir = %q, want %q", h.SkillDir, tt.want)
			}
		})
	}
}

func TestByName_Unknown(t *testing.T) {
	withIsolatedHome(t)
	if _, ok := ByName("not-a-real-harness"); ok {
		t.Error("ByName('not-a-real-harness') should return !ok")
	}
}

func TestStringRendering(t *testing.T) {
	cases := []struct {
		name    string
		h       Harness
		wantSub []string
	}{
		{
			"detected with binary",
			Harness{DisplayName: "Foo", Detected: true, BinaryPath: "/bin/foo"},
			[]string{"Foo", "detected", "/bin/foo"},
		},
		{
			"not detected",
			Harness{DisplayName: "Bar", Detected: false},
			[]string{"Bar", "not detected"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.h.String()
			for _, want := range tt.wantSub {
				if !strings.Contains(s, want) {
					t.Errorf("String() = %q, want substring %q", s, want)
				}
			}
		})
	}
}
