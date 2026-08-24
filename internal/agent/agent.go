// Package agent bridges the praxis-harness SDK (TUI + headless runner) into the
// praxis-cli cobra command tree. It is the single import boundary between the
// two repos: cobra commands in cmd/ call these helpers, which delegate to
// praxis-harness's tui.Run / native.Run / skills.Run.
//
// All agent functionality is gated behind an experimental flag (PRAXIS_EXPERIMENTAL
// env var or --experimental CLI flag). When disabled, the chat/run commands
// print a message telling the user how to opt in.
//
// Two rules keep this bridge from rotting as the harness ships releases. First,
// every option struct ends in ExtraArgs: whatever the harness grows next is
// reachable as `praxis run -- -new-flag value` without a praxis-cli release.
// Second, subcommands the harness already dispatches on its own argv (plugin,
// mcp, slack, agents) are forwarded verbatim through RunNative rather than
// re-modelled here, so their flags cannot drift out of sync.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/Facets-cloud/praxis-harness/ai"
	native "github.com/Facets-cloud/praxis-harness/cli/native"
	harnessskills "github.com/Facets-cloud/praxis-harness/cli/skills"
	"github.com/Facets-cloud/praxis-harness/selfupdate"
	"github.com/Facets-cloud/praxis-harness/tui"
)

// experimentalEnvVar gates the agent commands behind an opt-in. The flag is
// also exposed as --experimental on the cobra subcommands.
const experimentalEnvVar = "PRAXIS_EXPERIMENTAL"

// harnessModulePath is the module whose version `praxis version` reports as the
// embedded agent. Kept as a constant because it is matched against build info at
// runtime, where a typo would silently report "unknown" forever.
const harnessModulePath = "github.com/Facets-cloud/praxis-harness"

// ErrExperimentalDisabled is returned when the agent commands are invoked
// without the experimental flag.
var ErrExperimentalDisabled = errors.New("agent commands are experimental; set PRAXIS_EXPERIMENTAL=1 or pass --experimental to enable")

// Enabled returns true if the experimental agent features are turned on.
// The env var is the single source of truth: the --experimental cobra flag
// sets it before any agent command runs.
func Enabled() bool {
	return os.Getenv(experimentalEnvVar) == "1" || os.Getenv(experimentalEnvVar) == "true"
}

// Enable sets the env var so downstream harness code sees the flag.
func Enable() {
	_ = os.Setenv(experimentalEnvVar, "1")
}

// CheckEnabled is a convenience for cobra RunE functions: returns
// ErrExperimentalDisabled when the experimental flag is off, with a hint.
func CheckEnabled() error {
	if Enabled() {
		return nil
	}
	return fmt.Errorf("%w\n\n  %s", ErrExperimentalDisabled,
		strings.Join([]string{
			"To try the experimental Praxis coding agent:",
			"  PRAXIS_EXPERIMENTAL=1 praxis chat",
			"  praxis chat --experimental",
		}, "\n  "))
}

// suppressHarnessUpgradeCheck switches off the harness's own release check
// before any harness code runs.
//
// The harness banner compares buildinfo.Version() — which, embedded here, is
// praxis-cli's version — against praxis-harness releases, and would tell a
// praxis user to go install a different binary. praxis-cli owns its own update
// path (`praxis update`, or the Homebrew cask), so the check is noise at best
// and wrong advice at worst. An operator who explicitly set the variable keeps
// their setting: this only supplies a default.
func suppressHarnessUpgradeCheck() {
	if os.Getenv(selfupdate.DisableEnv) == "" {
		_ = os.Setenv(selfupdate.DisableEnv, "1")
	}
}

// HarnessVersion reports the praxis-harness release compiled into this binary,
// read from the module graph Go embeds rather than from a linker stamp — so it
// stays honest without any build-flag coupling.
//
// Test binaries are the one place this reads "unknown": the Go toolchain records
// no dependency module info for them. Every shipped binary (go build, go install,
// goreleaser) carries the graph.
func HarnessVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return harnessVersionFrom(info)
}

// harnessVersionFrom is the pure half of HarnessVersion, so the replaced and
// missing cases are testable without a build that exhibits them. A local
// `replace` to a working copy reports "(devel)" and an absent dependency reports
// "unknown": neither is dressed up as a release.
func harnessVersionFrom(info *debug.BuildInfo) string {
	if info == nil {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep == nil || dep.Path != harnessModulePath {
			continue
		}
		if dep.Replace != nil {
			if v := strings.TrimSpace(dep.Replace.Version); v != "" {
				return v
			}
			return "(devel)"
		}
		if v := strings.TrimSpace(dep.Version); v != "" {
			return v
		}
		return "(devel)"
	}
	return "unknown"
}

// ChatOptions are the flags for the interactive TUI agent (praxis chat).
type ChatOptions struct {
	// AgentsView starts the TUI on its session dashboard instead of a single chat
	// session — the same view the harness's own `prx agents` opens. It is a start
	// view, not a separate mode: the dashboard opens rows in the same in-process
	// client. Note `praxis agents` is a DIFFERENT, non-agent command in this CLI
	// (it lists installed agent files), so the dashboard is exposed as a flag.
	AgentsView     bool
	Model          string
	Thinking       string
	PermissionMode string
	Cwd            string
	SessionID      string
	SessionDir     string
	Resume         string
	TeamName       string
	SafeMode       bool
	Ephemeral      bool
	McpConfig      string
	SettingsPath   string
	Profile        string
	FallbackModels string
	Prompt         string
	MaxTurns       int
	// Extensions, GoProviders and AddDirs are repeatable in the harness for the
	// same reason: a path may contain a comma, so CSV splitting would shred one
	// root into two that exist nowhere.
	Extensions  []string
	GoProviders []string
	AddDirs     []string
	// ExtraArgs are harness flags this CLI does not model, forwarded verbatim
	// (everything after `--`).
	ExtraArgs []string
}

// RunChat launches the interactive Bubble Tea TUI, on a single chat session or on
// the session dashboard (ChatOptions.AgentsView). It maps ChatOptions to the
// harness's tui.Config (via tui.ParseFlags on a synthesized flag slice) and
// calls tui.Run. Blocks until the user exits the TUI.
func RunChat(ctx context.Context, opts ChatOptions) error {
	suppressHarnessUpgradeCheck()
	args := chatOptsToArgs(opts)
	cfg := tui.ParseFlags(args)
	return tui.Run(ctx, cfg)
}

// RunNative executes the harness's native CLI in-process: the headless one-shot
// runner, its `plugin` / `mcp` / `slack` / `agents` subcommands, and its `-acp`
// and `-sdk` server modes. args excludes the program name. Returns the exit code.
func RunNative(ctx context.Context, args []string) int {
	suppressHarnessUpgradeCheck()
	argv := append([]string{"praxis"}, args...)
	return native.Run(ctx, argv)
}

// RunSkillsReport renders the harness's read-only skill usage report — which
// skills the agent actually activated, and which have gone idle long enough to
// deserve a look. It is a separate harness entry point from RunNative because
// the report deliberately builds a client with MCP, LSP and session persistence
// switched off: reporting must not spawn stdio children to read local analytics.
func RunSkillsReport(ctx context.Context, args []string, stdout, stderr *os.File) int {
	suppressHarnessUpgradeCheck()
	return harnessskills.Run(ctx, args, harnessskills.DefaultLoader, stdout, stderr)
}

// NativeDialectArgs reports whether argv is the harness's process-sandbox child
// invocation — `<self> -provider … -sandbox-child -result-json` — and returns the
// arguments to hand to RunNative.
//
// It exists because the harness resolves that child by looking for a sibling or
// PATH binary named prx / praxis-native and, finding neither next to a binary
// called praxis, falls back to re-execing with bare native flags. Cobra rejects
// those outright ("unknown shorthand flag"), so process isolation would fail with
// a parse error instead of running.
//
// Recognition matches the SHAPE of that call site, not the presence of a magic
// flag. The child command line is bare flags from the first argument onward, and
// carries both -sandbox-child (the child registers only the sandbox tool set)
// and -result-json (the parent parses its single JSON object back) — see the
// harness's praxis/native_process_sandbox.go childArgs. A praxis command line, by
// contrast, always starts with a subcommand, so anchoring on argv[1] keeps
// `praxis run … -- -sandbox-child -result-json` — those flags arriving through the
// passthrough escape hatch — on the cobra path where the gate applies, and keeps
// a mistyped flag there too, where it gets a real error message.
//
// The experimental gate deliberately does not apply here, and cannot: the
// harness builds the child's environment from scratch — PATH, LANG and the
// provider key only (sandboxChildEnvironment) — so PRAXIS_EXPERIMENTAL cannot
// reach the child even though the parent that spawned it had already passed the
// gate. The gate is a feature switch, not a privilege boundary: this path grants
// nothing that PRAXIS_EXPERIMENTAL=1 would not, and the child still needs a
// provider credential in that scrubbed environment to do any work.
func NativeDialectArgs(argv []string) ([]string, bool) {
	if len(argv) < 3 || !strings.HasPrefix(argv[1], "-") {
		return nil, false
	}
	var sandboxChild, resultJSON bool
	for _, arg := range argv[1:] {
		switch arg {
		case "-sandbox-child", "--sandbox-child":
			sandboxChild = true
		case "-result-json", "--result-json":
			resultJSON = true
		}
	}
	if sandboxChild && resultJSON {
		return argv[1:], true
	}
	return nil, false
}

// chatOptsToArgs converts ChatOptions to a []string that tui.ParseFlags can
// consume. tui.Config fields are unexported, so we go through ParseFlags rather
// than constructing a Config directly. This keeps the bridge resilient to
// Config field additions in the harness — new fields just need new flag
// entries here, or none at all when the caller uses ExtraArgs.
func chatOptsToArgs(opts ChatOptions) []string {
	var args []string
	// tui.ParseFlags selects the dashboard start view from a LEADING positional
	// "agents", which it consumes before parsing flags. It must therefore be
	// args[0]; anywhere else it is an unparsed trailing positional and the TUI
	// silently opens a normal chat session instead.
	if opts.AgentsView {
		args = append(args, "agents")
	}
	add := func(flag, val string) {
		if val != "" {
			args = append(args, flag, val)
		}
	}
	addEach := func(flag string, vals []string) {
		for _, val := range vals {
			if strings.TrimSpace(val) != "" {
				args = append(args, flag, val)
			}
		}
	}
	add("-model", opts.Model)
	add("-thinking", opts.Thinking)
	add("-permission-mode", opts.PermissionMode)
	add("-cwd", opts.Cwd)
	add("-session-id", opts.SessionID)
	add("-session-dir", opts.SessionDir)
	add("-resume", opts.Resume)
	add("-team-name", opts.TeamName)
	if opts.SafeMode {
		args = append(args, "-safe")
	}
	if opts.Ephemeral {
		args = append(args, "-ephemeral")
	}
	add("-mcp-config", opts.McpConfig)
	add("-settings", opts.SettingsPath)
	add("-profile", opts.Profile)
	add("-fallback-models", opts.FallbackModels)
	add("-prompt", opts.Prompt)
	if opts.MaxTurns > 0 {
		args = append(args, "-max-turns", strconv.Itoa(opts.MaxTurns))
	}
	addEach("-extension", opts.Extensions)
	addEach("-go-provider", opts.GoProviders)
	addEach("-add-dir", opts.AddDirs)
	return append(args, opts.ExtraArgs...)
}

// HeadlessArgs converts a set of headless options to the argv slice that
// native.Run expects (without the leading program name).
type HeadlessArgs struct {
	Prompt      string
	PromptFile  string
	HistoryFile string
	Model       string
	Provider    string
	Models      string
	FallbackAny string
	Thinking    string
	Session     string
	SessionDir  string
	ForkFrom    string
	Cwd         string
	Profile     string
	CacheKey    string

	// Output.
	ResultJSON       bool
	UsageJSON        bool
	OutputSchema     string
	OutputSchemaName string
	TurnRecap        bool

	// Discovery and prompt shaping.
	SystemPrompt       string
	AppendSystemPrompt string
	SkillsDir          string
	SkillsProjectOnly  bool
	Configs            string
	Extensions         string
	NoExtensions       bool
	Hooks              string
	Personality        string
	OutputStyle        string
	Advisor            bool

	// Tools, MCP and containment.
	Tools             string
	NoTools           bool
	NoLSP             bool
	NoSkills          bool
	NoRules           bool
	NoMCP             bool
	McpConfig         string
	SettingsPath      string
	PermissionMode    string
	PermissionRules   []string
	AddDirs           []string
	Sandbox           string
	SandboxExecutable string
	SafeMode          bool
	AllowHome         bool
	Destructive       string
	EgressDefault     string
	EgressAllow       string
	EgressDeny        string

	// Budgets and behaviour toggles. The tri-state strings take "true"/"false";
	// empty leaves the harness default, which is NOT the same as false.
	MaxTurns             int
	MaxTokenBudget       int
	MaxOutputTokens      int
	MaxTime              int
	SubagentSubscription string
	RtkRewrite           string
	ReflexCapture        string
	NoAuthCheck          bool

	// ExtraArgs are harness flags this CLI does not model, forwarded verbatim
	// (everything after `--`).
	ExtraArgs []string
}

// ToNativeArgs converts HeadlessArgs to the flag slice that native.Run parses.
func (h HeadlessArgs) ToNativeArgs() []string {
	var args []string
	add := func(flag, val string) {
		if val != "" {
			args = append(args, flag, val)
		}
	}
	addBool := func(flag string, val bool) {
		if val {
			args = append(args, flag)
		}
	}
	addInt := func(flag string, val int) {
		if val > 0 {
			args = append(args, flag, strconv.Itoa(val))
		}
	}
	addEach := func(flag string, vals []string) {
		for _, val := range vals {
			if strings.TrimSpace(val) != "" {
				args = append(args, flag, val)
			}
		}
	}

	add("-prompt", h.Prompt)
	add("-prompt-file", h.PromptFile)
	add("-history-file", h.HistoryFile)
	add("-model", h.Model)
	add("-provider", h.Provider)
	add("-models", h.Models)
	add("-fallback-model", h.FallbackAny)
	add("-thinking", h.Thinking)
	add("-session", h.Session)
	add("-session-dir", h.SessionDir)
	add("-fork-from", h.ForkFrom)
	add("-cwd", h.Cwd)
	add("-profile", h.Profile)
	add("-cache-key", h.CacheKey)

	addBool("-result-json", h.ResultJSON)
	addBool("-usage-json", h.UsageJSON)
	add("-output-schema", h.OutputSchema)
	add("-output-schema-name", h.OutputSchemaName)
	addBool("-turn-recap", h.TurnRecap)

	add("-system-prompt", h.SystemPrompt)
	add("-append-system-prompt", h.AppendSystemPrompt)
	add("-skills", h.SkillsDir)
	addBool("-skills-project-only", h.SkillsProjectOnly)
	add("-config", h.Configs)
	add("-extension", h.Extensions)
	addBool("-no-extensions", h.NoExtensions)
	add("-hook", h.Hooks)
	add("-personality", h.Personality)
	add("-output-style", h.OutputStyle)
	addBool("-advisor", h.Advisor)

	add("-tools", h.Tools)
	addBool("-no-tools", h.NoTools)
	addBool("-no-lsp", h.NoLSP)
	addBool("-no-skills", h.NoSkills)
	addBool("-no-rules", h.NoRules)
	addBool("-no-mcp", h.NoMCP)
	add("-mcp-config", h.McpConfig)
	add("-settings", h.SettingsPath)
	add("-permission-mode", h.PermissionMode)
	addEach("-permission-rule", h.PermissionRules)
	addEach("-add-dir", h.AddDirs)
	add("-sandbox", h.Sandbox)
	add("-sandbox-exec", h.SandboxExecutable)
	addBool("-safe-mode", h.SafeMode)
	addBool("-allow-home", h.AllowHome)
	add("-destructive", h.Destructive)
	add("-egress-default", h.EgressDefault)
	add("-egress-allow", h.EgressAllow)
	add("-egress-deny", h.EgressDeny)

	addInt("-max-turns", h.MaxTurns)
	addInt("-max-token-budget", h.MaxTokenBudget)
	addInt("-max-output-tokens", h.MaxOutputTokens)
	addInt("-max-time", h.MaxTime)
	add("-subagent-subscription", h.SubagentSubscription)
	add("-rtk-rewrite", h.RtkRewrite)
	add("-reflex-capture", h.ReflexCapture)
	addBool("-no-auth-check", h.NoAuthCheck)

	return append(args, h.ExtraArgs...)
}

// SelfSandboxExecutable returns the path to this binary, for use as the
// process-sandbox child. The harness looks for a sibling or PATH `prx` /
// `praxis-native` and cannot find one next to a binary named `praxis`, so
// `praxis run --sandbox process` points it at itself; NativeDialectArgs then
// routes the re-exec into the native runner.
func SelfSandboxExecutable() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	return self
}

// Logo returns the Praxis pixel-art logo from the harness TUI package.
// Used by the help/branding output in praxis-cli.
func Logo() string {
	return tui.PraxisLogo()
}

// ResolveModel resolves a model name to its provider using the harness's
// model registry. Returns the resolved model ID and provider name.
func ResolveModel(model string) (provider, resolved string, err error) {
	resolvedModel, resolvedProvider, ok := ai.ResolveModelName(model)
	if !ok {
		return "", "", fmt.Errorf("unknown model %q", model)
	}
	return resolvedProvider, resolvedModel, nil
}
