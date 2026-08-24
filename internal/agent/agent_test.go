package agent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-harness/selfupdate"
)

func TestEnabledDefaultOff(t *testing.T) {
	t.Setenv(experimentalEnvVar, "")
	if Enabled() {
		t.Fatal("Enabled() = true, want false when PRAXIS_EXPERIMENTAL is unset")
	}
}

func TestEnabledEnvVar(t *testing.T) {
	t.Setenv(experimentalEnvVar, "1")
	if !Enabled() {
		t.Fatal("Enabled() = false, want true when PRAXIS_EXPERIMENTAL=1")
	}
}

func TestEnabledEnvVarTrue(t *testing.T) {
	t.Setenv(experimentalEnvVar, "true")
	if !Enabled() {
		t.Fatal("Enabled() = false, want true when PRAXIS_EXPERIMENTAL=true")
	}
}

func TestCheckEnabledReturnsErrorWhenOff(t *testing.T) {
	t.Setenv(experimentalEnvVar, "")
	err := CheckEnabled()
	if err == nil {
		t.Fatal("CheckEnabled() = nil, want error when experimental is off")
	}
	if !errors.Is(err, ErrExperimentalDisabled) {
		t.Fatalf("CheckEnabled() err = %v, want ErrExperimentalDisabled", err)
	}
}

func TestCheckEnabledReturnsNilWhenOn(t *testing.T) {
	t.Setenv(experimentalEnvVar, "1")
	err := CheckEnabled()
	if err != nil {
		t.Fatalf("CheckEnabled() = %v, want nil when experimental is on", err)
	}
}

func TestChatOptsToArgs(t *testing.T) {
	opts := ChatOptions{
		Model:          "opus",
		Thinking:       "high",
		PermissionMode: "auto",
		Cwd:            ".",
		SafeMode:       true,
		MaxTurns:       10,
	}
	args := chatOptsToArgs(opts)

	want := []string{"-model", "opus", "-thinking", "high", "-permission-mode", "auto", "-cwd", ".", "-safe", "-max-turns", "10"}
	if len(args) != len(want) {
		t.Fatalf("chatOptsToArgs returned %v (len %d), want %v (len %d)", args, len(args), want, len(want))
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (full args: %v)", i, args[i], want[i], args)
		}
	}
}

// The dashboard start view rides on a LEADING positional that tui.ParseFlags
// consumes before parsing flags. If "agents" is not args[0] the harness treats it
// as a trailing positional and silently opens a normal chat session, so the
// position — not merely the presence — is the contract under test.
func TestChatOptsToArgsPutsAgentsViewFirst(t *testing.T) {
	args := chatOptsToArgs(ChatOptions{AgentsView: true, Model: "opus", Cwd: "/repo"})

	want := []string{"agents", "-model", "opus", "-cwd", "/repo"}
	if len(args) != len(want) {
		t.Fatalf("chatOptsToArgs = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (full args: %v)", i, args[i], want[i], args)
		}
	}

	if plain := chatOptsToArgs(ChatOptions{Model: "opus"}); len(plain) == 0 || plain[0] == "agents" {
		t.Fatalf("chat without --agents must not request the dashboard: %v", plain)
	}
}

func TestChatOptsToArgsEmpty(t *testing.T) {
	args := chatOptsToArgs(ChatOptions{})
	if len(args) != 0 {
		t.Fatalf("chatOptsToArgs(empty) = %v, want empty slice", args)
	}
}

func TestHeadlessArgsToNativeArgs(t *testing.T) {
	ha := HeadlessArgs{
		Prompt:   "hello",
		Model:    "opus",
		Cwd:      ".",
		NoMCP:    true,
		MaxTurns: 5,
	}
	args := ha.ToNativeArgs()

	// Verify each flag AND its value (not just presence).
	type kv struct{ flag, val string }
	want := []kv{
		{"-prompt", "hello"},
		{"-model", "opus"},
		{"-cwd", "."},
		{"-max-turns", "5"},
	}
	for _, w := range want {
		found := false
		for i, a := range args {
			if a == w.flag && i+1 < len(args) && args[i+1] == w.val {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ToNativeArgs missing %s %s, got %v", w.flag, w.val, args)
		}
	}
	// Boolean flags: verify -no-mcp is present (no value).
	hasNoMCP := false
	for _, a := range args {
		if a == "-no-mcp" {
			hasNoMCP = true
		}
	}
	if !hasNoMCP {
		t.Errorf("ToNativeArgs missing -no-mcp, got %v", args)
	}
}

func TestEnable(t *testing.T) {
	t.Setenv(experimentalEnvVar, "")
	Enable()
	if !Enabled() {
		t.Fatal("Enable() did not set PRAXIS_EXPERIMENTAL")
	}
}

func TestLogoNotEmpty(t *testing.T) {
	logo := Logo()
	if logo == "" {
		t.Fatal("Logo() returned empty string")
	}
}

// Every modelled field must map to a DISTINCT harness flag. Two fields sharing a
// flag name is the failure mode this catches: the argv still looks plausible, but
// one option silently overrides the other at parse time.
func TestHeadlessArgsFlagsAreDistinctAndComplete(t *testing.T) {
	ha := HeadlessArgs{
		Prompt: "p", PromptFile: "pf", HistoryFile: "hf", Model: "m", Provider: "pr",
		Models: "ms", FallbackAny: "fb", Thinking: "high", Session: "s", SessionDir: "sd",
		ForkFrom: "ff", Cwd: ".", Profile: "pf2", CacheKey: "ck",
		ResultJSON: true, UsageJSON: true, OutputSchema: "os", OutputSchemaName: "osn", TurnRecap: true,
		SystemPrompt: "sp", AppendSystemPrompt: "asp", SkillsDir: "sk", SkillsProjectOnly: true,
		Configs: "cfg", Extensions: "ext", NoExtensions: true, Hooks: "hk",
		Personality: "pers", OutputStyle: "style", Advisor: true,
		Tools: "tools", NoTools: true, NoLSP: true, NoSkills: true, NoRules: true, NoMCP: true,
		McpConfig: "mc", SettingsPath: "set", PermissionMode: "auto",
		PermissionRules: []string{"r1", "r2"}, AddDirs: []string{"/a", "/b"},
		Sandbox: "process", SandboxExecutable: "/bin/praxis", SafeMode: true, AllowHome: true,
		Destructive: "false", EgressDefault: "deny", EgressAllow: "a", EgressDeny: "d",
		MaxTurns: 5, MaxTokenBudget: 10, MaxOutputTokens: 20, MaxTime: 30,
		SubagentSubscription: "true", RtkRewrite: "true", ReflexCapture: "false", NoAuthCheck: true,
	}
	args := ha.ToNativeArgs()

	seen := map[string]int{}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			seen[arg]++
		}
	}
	for flag, count := range seen {
		// Repeatable flags are the only legitimate duplicates.
		if count > 1 && flag != "-permission-rule" && flag != "-add-dir" {
			t.Errorf("flag %s emitted %d times; two fields map to the same harness flag", flag, count)
		}
	}

	// Spot-check the pairs most likely to be mistyped or transposed.
	for _, want := range [][2]string{
		{"-history-file", "hf"}, {"-fallback-model", "fb"}, {"-cache-key", "ck"},
		{"-output-schema", "os"}, {"-append-system-prompt", "asp"}, {"-sandbox", "process"},
		{"-sandbox-exec", "/bin/praxis"}, {"-max-output-tokens", "20"}, {"-max-time", "30"},
		{"-egress-default", "deny"}, {"-subagent-subscription", "true"},
	} {
		if !hasPair(args, want[0], want[1]) {
			t.Errorf("missing %s %s in %v", want[0], want[1], args)
		}
	}
	for _, flag := range []string{"-no-tools", "-no-lsp", "-no-skills", "-no-rules", "-safe-mode", "-allow-home", "-turn-recap", "-advisor", "-no-auth-check", "-skills-project-only", "-no-extensions"} {
		if seen[flag] != 1 {
			t.Errorf("boolean flag %s not emitted: %v", flag, args)
		}
	}
	// Repeatables must emit one flag per value, not a joined string: a directory
	// name may contain a comma.
	if !hasPair(args, "-add-dir", "/a") || !hasPair(args, "-add-dir", "/b") {
		t.Errorf("repeatable -add-dir lost a value: %v", args)
	}
}

func hasPair(args []string, flag, val string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == val {
			return true
		}
	}
	return false
}

// ExtraArgs is the escape hatch that keeps this bridge from gating harness
// features behind a praxis release. It has to land LAST, because the harness flag
// parser takes the last occurrence of a repeated flag — that is what lets a user
// override a value this CLI modelled.
func TestExtraArgsComeLast(t *testing.T) {
	native := HeadlessArgs{Prompt: "hi", ExtraArgs: []string{"-reflex-capture", "true"}}.ToNativeArgs()
	if got := native[len(native)-2:]; got[0] != "-reflex-capture" || got[1] != "true" {
		t.Fatalf("headless ExtraArgs not last: %v", native)
	}

	chat := chatOptsToArgs(ChatOptions{Model: "opus", ExtraArgs: []string{"-no-lsp"}})
	if chat[len(chat)-1] != "-no-lsp" {
		t.Fatalf("chat ExtraArgs not last: %v", chat)
	}
}

func TestHeadlessArgsEmptyEmitsNothing(t *testing.T) {
	if args := (HeadlessArgs{}).ToNativeArgs(); len(args) != 0 {
		t.Fatalf("ToNativeArgs(empty) = %v, want no args so the harness defaults stand", args)
	}
}

// Repeatable, not comma-joined: the harness accepts these flags multiple times
// precisely because a path may contain a comma.
func TestChatOptsToArgsRepeatables(t *testing.T) {
	args := chatOptsToArgs(ChatOptions{
		Extensions:  []string{"/ext/a", "", "/ext/b"},
		GoProviders: []string{"/gp"},
		AddDirs:     []string{"/dir,with,commas"},
	})
	want := []string{
		"-extension", "/ext/a", "-extension", "/ext/b",
		"-go-provider", "/gp",
		"-add-dir", "/dir,with,commas",
	}
	if len(args) != len(want) {
		t.Fatalf("chatOptsToArgs = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (full: %v)", i, args[i], want[i], args)
		}
	}
}

// The harness re-execs this binary as a process-sandbox child using bare native
// flags. Cobra cannot parse that dialect, so main() has to recognize it — and
// must NOT hijack an ordinary invocation, which would take the whole CLI down.
func TestNativeDialectArgs(t *testing.T) {
	child := []string{"/usr/local/bin/praxis", "-provider", "anthropic", "-sandbox-child", "-result-json"}
	args, ok := NativeDialectArgs(child)
	if !ok {
		t.Fatalf("NativeDialectArgs(%v) = false, want true", child)
	}
	if len(args) != len(child)-1 || args[0] != "-provider" {
		t.Fatalf("args = %v, want the argv tail without the program name", args)
	}

	for _, argv := range [][]string{
		{"praxis"},
		{"praxis", "chat", "--experimental"},
		{"praxis", "run", "--prompt", "sandbox-child"},
		{"praxis", "agent", "sessions"},
	} {
		if _, ok := NativeDialectArgs(argv); ok {
			t.Errorf("NativeDialectArgs(%v) = true, want false: cobra must keep handling normal invocations", argv)
		}
	}
}

// A bug report against `praxis chat` is only actionable with the agent version,
// which ships on its own cadence. The lookup walks the module graph, so each of
// its outcomes is asserted here against a synthetic graph: the Go toolchain
// records no dependency modules for test binaries, which is why the real
// HarnessVersion() reads "unknown" under `go test` and only there.
func TestHarnessVersionFrom(t *testing.T) {
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{
			name: "released dependency",
			info: &debug.BuildInfo{Deps: []*debug.Module{
				{Path: "github.com/spf13/cobra", Version: "v1.10.2"},
				{Path: harnessModulePath, Version: "v0.5.4"},
			}},
			want: "v0.5.4",
		},
		{
			name: "replaced by a working copy",
			info: &debug.BuildInfo{Deps: []*debug.Module{
				{Path: harnessModulePath, Version: "v0.0.0", Replace: &debug.Module{Path: "/src/praxis-harness"}},
			}},
			want: "(devel)",
		},
		{
			name: "replaced by another version",
			info: &debug.BuildInfo{Deps: []*debug.Module{
				{Path: harnessModulePath, Version: "v0.1.1", Replace: &debug.Module{Path: harnessModulePath, Version: "v0.5.4"}},
			}},
			want: "v0.5.4",
		},
		{
			name: "dependency absent",
			info: &debug.BuildInfo{Deps: []*debug.Module{{Path: "github.com/spf13/cobra", Version: "v1.10.2"}}},
			want: "unknown",
		},
		{name: "no build info", info: nil, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := harnessVersionFrom(tc.info); got != tc.want {
				t.Fatalf("harnessVersionFrom = %q, want %q", got, tc.want)
			}
		})
	}
}

// The module path is matched as a string at runtime, so a rename in go.mod would
// silently turn every version report into "unknown" with nothing failing.
func TestHarnessModulePathMatchesGoMod(t *testing.T) {
	gomod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(gomod), harnessModulePath+" v") {
		t.Fatalf("go.mod has no %q requirement; the version lookup would report \"unknown\" forever", harnessModulePath)
	}
}

// praxis-cli owns its update path, so the harness's own release banner is turned
// off — but never over an operator's explicit choice.
func TestSuppressHarnessUpgradeCheck(t *testing.T) {
	t.Setenv(selfupdate.DisableEnv, "")
	suppressHarnessUpgradeCheck()
	if got := os.Getenv(selfupdate.DisableEnv); got != "1" {
		t.Fatalf("%s = %q, want \"1\"", selfupdate.DisableEnv, got)
	}

	t.Setenv(selfupdate.DisableEnv, "0")
	suppressHarnessUpgradeCheck()
	if got := os.Getenv(selfupdate.DisableEnv); got != "0" {
		t.Fatalf("%s = %q, want the operator's explicit \"0\" preserved", selfupdate.DisableEnv, got)
	}
}

func TestSelfSandboxExecutable(t *testing.T) {
	if SelfSandboxExecutable() == "" {
		t.Fatal("SelfSandboxExecutable() = \"\"; --sandbox process would fall back to a prx lookup that cannot succeed")
	}
}
