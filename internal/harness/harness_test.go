package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withIsolatedHome redirects $HOME to a temp dir AND clears $PATH so that
// exec.LookPath returns "not found" for claude/cursor/gemini binaries.
// This gives us a deterministic baseline: no harness should be detected.
func withIsolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	return home
}

func TestAll_ReturnsAllSupportedHarnesses(t *testing.T) {
	withIsolatedHome(t)
	got := All()
	if len(got) != 3 {
		t.Errorf("len(All()) = %d, want 3", len(got))
	}
	names := map[string]bool{}
	for _, h := range got {
		names[h.Name] = true
	}
	for _, want := range []string{"claude-code", "cursor", "gemini-cli"} {
		if !names[want] {
			t.Errorf("All() missing harness %q", want)
		}
	}
}

func TestDetect_NoSignals(t *testing.T) {
	withIsolatedHome(t)
	for _, h := range All() {
		if h.Detected {
			t.Errorf("%s should NOT be detected on a clean machine: %+v", h.Name, h)
		}
	}
}

func TestDetect_ClaudeCodeViaHomeDir(t *testing.T) {
	home := withIsolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	h, ok := ByName("claude-code")
	if !ok {
		t.Fatal("ByName(claude-code) returned !ok")
	}
	if !h.Detected {
		t.Errorf("Claude Code not detected when ~/.claude exists: %+v", h)
	}
}

func TestDetect_CursorViaHomeDir(t *testing.T) {
	home := withIsolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0700); err != nil {
		t.Fatal(err)
	}
	h, ok := ByName("cursor")
	if !ok {
		t.Fatal("ByName(cursor) returned !ok")
	}
	if !h.Detected {
		t.Errorf("Cursor not detected when ~/.cursor exists: %+v", h)
	}
}

func TestDetect_GeminiCLIViaHomeDir(t *testing.T) {
	home := withIsolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0700); err != nil {
		t.Fatal(err)
	}
	h, ok := ByName("gemini-cli")
	if !ok {
		t.Fatal("ByName(gemini-cli) returned !ok")
	}
	if !h.Detected {
		t.Errorf("Gemini CLI not detected when ~/.gemini exists: %+v", h)
	}
}

func TestSkillDir_PerHarness(t *testing.T) {
	home := withIsolatedHome(t)
	tests := []struct {
		name     string
		wantPath string
	}{
		{"claude-code", filepath.Join(home, ".claude", "skills")},
		{"cursor", filepath.Join(home, ".cursor", "rules")},
		{"gemini-cli", filepath.Join(home, ".gemini", "skills")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ok := ByName(tt.name)
			if !ok {
				t.Fatalf("ByName(%s) returned !ok", tt.name)
			}
			if h.SkillDir != tt.wantPath {
				t.Errorf("SkillDir = %q, want %q", h.SkillDir, tt.wantPath)
			}
		})
	}
}

func TestByName_Unknown(t *testing.T) {
	withIsolatedHome(t)
	_, ok := ByName("not-a-real-harness")
	if ok {
		t.Errorf("ByName('not-a-real-harness') should return !ok")
	}
}

func TestDetected_FiltersToOnlyDetected(t *testing.T) {
	home := withIsolatedHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
		t.Fatal(err)
	}
	got := Detected()
	if len(got) != 1 {
		t.Errorf("len(Detected()) = %d, want 1 (only claude-code marked)", len(got))
	}
	if len(got) >= 1 && got[0].Name != "claude-code" {
		t.Errorf("Detected()[0].Name = %q, want claude-code", got[0].Name)
	}
}

func TestStringRendering(t *testing.T) {
	tests := []struct {
		name    string
		h       Harness
		wantSub []string
	}{
		{
			"detected with binary",
			Harness{DisplayName: "Foo", Detected: true, BinaryPath: "/usr/bin/foo"},
			[]string{"Foo", "detected", "/usr/bin/foo"},
		},
		{
			"not detected",
			Harness{DisplayName: "Bar", Detected: false},
			[]string{"Bar", "not detected"},
		},
	}
	for _, tt := range tests {
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
