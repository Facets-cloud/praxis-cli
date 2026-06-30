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

func TestAll_ReturnsAllHarnesses(t *testing.T) {
	withIsolatedHome(t)
	got := All()
	if len(got) != 4 {
		t.Errorf("len(All()) = %d, want 4", len(got))
	}
	wantNames := map[string]bool{"claude-code": true, "codex": true, "gemini-cli": true, "antigravity": true}
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

func TestDetect_Antigravity_AppDataDir(t *testing.T) {
	home := withIsolatedHome(t)
	// The antigravity-ide app-data subdir under ~/.gemini is the distinct
	// signal that disambiguates Antigravity from Gemini CLI.
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-ide"), 0700); err != nil {
		t.Fatal(err)
	}
	h, _ := ByName("antigravity")
	if !h.Detected {
		t.Errorf("Antigravity not detected when ~/.gemini/antigravity-ide exists: %+v", h)
	}
}

func TestDetect_Antigravity_NotOnBareGemini(t *testing.T) {
	home := withIsolatedHome(t)
	// A bare ~/.gemini (Gemini CLI's signal) must NOT trip Antigravity —
	// the two share the ~/.gemini root and must stay disambiguated.
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0700); err != nil {
		t.Fatal(err)
	}
	h, _ := ByName("antigravity")
	if h.Detected {
		t.Errorf("Antigravity must not be detected on a bare ~/.gemini: %+v", h)
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
		{"antigravity", filepath.Join(home, ".gemini", "config", "skills")},
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

// TestProjectScoped covers ProjectScoped's path-rebasing across harness
// shapes: a home-relative harness (claude-code), one whose skill and
// agent dirs live under different dotdirs (codex — must rebase each
// independently), and a non-home harness (paths left untouched). Cases
// that pass checkOriginal also assert the value receiver leaves the
// original harness user-level.
func TestProjectScoped(t *testing.T) {
	home := withIsolatedHome(t)
	tests := []struct {
		name          string
		harness       Harness
		wantSkill     string
		wantAgent     string
		origSkill     string // expected original SkillDir when checkOriginal
		origAgent     string // expected original AgentDir when checkOriginal
		checkOriginal bool
	}{
		{
			name:          "claude-code rebases home-relative dirs",
			harness:       mustByName(t, "claude-code"),
			wantSkill:     filepath.Join(".claude", "skills"),
			wantAgent:     filepath.Join(".claude", "agents"),
			origSkill:     filepath.Join(home, ".claude", "skills"),
			origAgent:     filepath.Join(home, ".claude", "agents"),
			checkOriginal: true,
		},
		{
			// Codex splits skills (~/.agents/skills) and agents
			// (~/.codex/agents) across different dotdirs — each must rebase
			// independently, not assume a shared base.
			name:      "codex rebases split dotdirs independently",
			harness:   mustByName(t, "codex"),
			wantSkill: filepath.Join(".agents", "skills"),
			wantAgent: filepath.Join(".codex", "agents"),
		},
		{
			// Paths not under the user's home are left unchanged.
			name:      "non-home dirs left unchanged",
			harness:   Harness{Name: "x", SkillDir: "/etc/skills", AgentDir: "/etc/agents"},
			wantSkill: "/etc/skills",
			wantAgent: "/etc/agents",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proj := t.TempDir()
			wantSkill, wantAgent := tt.wantSkill, tt.wantAgent
			if !filepath.IsAbs(wantSkill) {
				wantSkill = filepath.Join(proj, wantSkill)
			}
			if !filepath.IsAbs(wantAgent) {
				wantAgent = filepath.Join(proj, wantAgent)
			}
			scoped := tt.harness.ProjectScoped(proj)
			if scoped.SkillDir != wantSkill {
				t.Errorf("scoped SkillDir = %q, want %q", scoped.SkillDir, wantSkill)
			}
			if scoped.AgentDir != wantAgent {
				t.Errorf("scoped AgentDir = %q, want %q", scoped.AgentDir, wantAgent)
			}
			if tt.checkOriginal {
				// Value receiver: the original must be untouched.
				if tt.harness.SkillDir != tt.origSkill {
					t.Errorf("receiver mutated: SkillDir = %q, want %q", tt.harness.SkillDir, tt.origSkill)
				}
				if tt.harness.AgentDir != tt.origAgent {
					t.Errorf("receiver mutated: AgentDir = %q, want %q", tt.harness.AgentDir, tt.origAgent)
				}
			}
		})
	}
}

// mustByName looks up a harness by name or fails the test.
func mustByName(t *testing.T, name string) Harness {
	t.Helper()
	h, ok := ByName(name)
	if !ok {
		t.Fatalf("ByName(%q) = !ok", name)
	}
	return h
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

func TestAllHarnessesHaveAgentDir(t *testing.T) {
	home := withIsolatedHome(t)
	want := map[string]string{
		"claude-code": filepath.Join(home, ".claude", "agents"),
		"codex":       filepath.Join(home, ".codex", "agents"),
		"gemini-cli":  filepath.Join(home, ".gemini", "agents"),
		"antigravity": filepath.Join(home, ".gemini", "config", "agents"),
	}
	for _, h := range All() {
		got, ok := want[h.Name]
		if !ok {
			t.Errorf("unexpected harness %q", h.Name)
			continue
		}
		if h.AgentDir != got {
			t.Errorf("harness %q AgentDir = %q, want %q", h.Name, h.AgentDir, got)
		}
	}
}
