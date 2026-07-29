// Package agent bridges the praxis-harness SDK (TUI + headless runner) into the
// praxis-cli cobra command tree. It is the single import boundary between the
// two repos: cobra commands in cmd/ call these helpers, which delegate to
// praxis-harness's tui.Run / native.Run.
//
// All agent functionality is gated behind an experimental flag (PRAXIS_EXPERIMENTAL
// env var or --experimental CLI flag). When disabled, the chat/run commands
// print a message telling the user how to opt in.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Facets-cloud/praxis-harness/ai"
	native "github.com/Facets-cloud/praxis-harness/cli/native"
	"github.com/Facets-cloud/praxis-harness/tui"
)

// experimentalEnvVar gates the agent commands behind an opt-in. The flag is
// also exposed as --experimental on the cobra subcommands.
const experimentalEnvVar = "PRAXIS_EXPERIMENTAL"

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

// ChatOptions are the flags for the interactive TUI agent (praxis chat).
type ChatOptions struct {
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
}

// RunChat launches the interactive Bubble Tea TUI. It maps ChatOptions to the
// harness's tui.Config (via tui.ParseFlags on a synthesized flag slice) and
// calls tui.Run. Blocks until the user exits the TUI.
func RunChat(ctx context.Context, opts ChatOptions) error {
	args := chatOptsToArgs(opts)
	cfg := tui.ParseFlags(args)
	return tui.Run(ctx, cfg)
}

// RunHeadless executes a one-shot prompt through the headless native runner.
// It delegates to native.Run with a synthesized argv, exactly like the prx run
// subcommand. Returns the exit code.
func RunHeadless(ctx context.Context, args []string) int {
	argv := append([]string{"praxis"}, args...)
	return native.Run(ctx, argv)
}

// chatOptsToArgs converts ChatOptions to a []string that tui.ParseFlags can
// consume. tui.Config fields are unexported, so we go through ParseFlags rather
// than constructing a Config directly. This keeps the bridge resilient to
// Config field additions in the harness — new fields just need new flag
// entries here.
func chatOptsToArgs(opts ChatOptions) []string {
	var args []string
	if opts.Model != "" {
		args = append(args, "-model", opts.Model)
	}
	if opts.Thinking != "" {
		args = append(args, "-thinking", opts.Thinking)
	}
	if opts.PermissionMode != "" {
		args = append(args, "-permission-mode", opts.PermissionMode)
	}
	if opts.Cwd != "" {
		args = append(args, "-cwd", opts.Cwd)
	}
	if opts.SessionID != "" {
		args = append(args, "-session-id", opts.SessionID)
	}
	if opts.SessionDir != "" {
		args = append(args, "-session-dir", opts.SessionDir)
	}
	if opts.Resume != "" {
		args = append(args, "-resume", opts.Resume)
	}
	if opts.TeamName != "" {
		args = append(args, "-team-name", opts.TeamName)
	}
	if opts.SafeMode {
		args = append(args, "-safe")
	}
	if opts.Ephemeral {
		args = append(args, "-ephemeral")
	}
	if opts.McpConfig != "" {
		args = append(args, "-mcp-config", opts.McpConfig)
	}
	if opts.SettingsPath != "" {
		args = append(args, "-settings", opts.SettingsPath)
	}
	if opts.Profile != "" {
		args = append(args, "-profile", opts.Profile)
	}
	if opts.FallbackModels != "" {
		args = append(args, "-fallback-models", opts.FallbackModels)
	}
	if opts.Prompt != "" {
		args = append(args, "-prompt", opts.Prompt)
	}
	if opts.MaxTurns > 0 {
		args = append(args, "-max-turns", fmt.Sprintf("%d", opts.MaxTurns))
	}
	return args
}

// HeadlessArgs converts a set of headless options to the argv slice that
// native.Run expects (without the leading program name).
type HeadlessArgs struct {
	Prompt         string
	PromptFile     string
	Model          string
	Provider       string
	Thinking       string
	Session        string
	ForkFrom       string
	Cwd            string
	ResultJSON     bool
	UsageJSON      bool
	NoMCP          bool
	McpConfig      string
	SettingsPath   string
	MaxTurns       int
	MaxTokenBudget int
}

// ToNativeArgs converts HeadlessArgs to the flag slice that native.Run parses.
func (h HeadlessArgs) ToNativeArgs() []string {
	var args []string
	add := func(flag, val string) {
		if val != "" {
			args = append(args, flag, val)
		}
	}
	add("-prompt", h.Prompt)
	add("-prompt-file", h.PromptFile)
	add("-model", h.Model)
	add("-provider", h.Provider)
	add("-thinking", h.Thinking)
	add("-session", h.Session)
	add("-fork-from", h.ForkFrom)
	add("-cwd", h.Cwd)
	add("-mcp-config", h.McpConfig)
	add("-settings", h.SettingsPath)
	if h.ResultJSON {
		args = append(args, "-result-json")
	}
	if h.UsageJSON {
		args = append(args, "-usage-json")
	}
	if h.NoMCP {
		args = append(args, "-no-mcp")
	}
	if h.MaxTurns > 0 {
		args = append(args, "-max-turns", fmt.Sprintf("%d", h.MaxTurns))
	}
	if h.MaxTokenBudget > 0 {
		args = append(args, "-max-token-budget", fmt.Sprintf("%d", h.MaxTokenBudget))
	}
	return args
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

// Ensure context is referenced for the linter even if future callers
// don't use it yet — RunChat and RunHeadless take ctx.
var _ = context.Background
