package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Facets-cloud/praxis-cli/internal/agent"
	"github.com/spf13/cobra"
)

// runOpts is bound directly by the cobra flags below, so there is no second
// copy of the flag set to keep in sync with agent.HeadlessArgs. Package-level
// state, like every other command here; tests reset it.
var (
	runOpts         agent.HeadlessArgs
	runExperimental bool
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run a one-shot prompt through the Praxis agent (experimental)",
	Long: `Execute a single prompt headlessly: build the agent, run the prompt to
completion, print the result, and exit. Designed for scripting, CI, and
automation pipelines.

This is an EXPERIMENTAL feature. Enable it with --experimental or
PRAXIS_EXPERIMENTAL=1.

The prompt can be provided via --prompt, --prompt-file, or piped through stdin
with --prompt -.

Anything after -- is passed to the agent runtime verbatim, so harness flags that
this CLI does not model are still reachable without waiting for a praxis release:

  praxis run --experimental --prompt "…" -- -reflex-capture true

Examples:
  praxis run --experimental --prompt "fix the failing test"
  praxis run --experimental --prompt - < input.txt
  praxis run --experimental --prompt-file task.txt --model opus
  praxis run --experimental --prompt "refactor" --result-json
  praxis run --experimental --prompt "audit deps" --sandbox process --add-dir ../lib`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          agentPassthroughArgs("praxis run", "pass the prompt with --prompt"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if runExperimental {
			agent.Enable()
		}
		// Returned, not printed here: Execute() renders it once.
		if err := agent.CheckEnabled(); err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		opts := runOpts
		opts.ExtraArgs = args
		// The process sandbox re-execs a child that speaks the harness's native
		// flag dialect. The harness looks for a sibling or PATH prx /
		// praxis-native and finds neither next to a binary called praxis, so
		// point it at ourselves; main() routes that dialect to the native runner.
		if opts.Sandbox == "process" && opts.SandboxExecutable == "" {
			opts.SandboxExecutable = agent.SelfSandboxExecutable()
		}

		exitCode := agentRunNative(ctx, opts.ToNativeArgs())
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	},
}

func init() {
	f := runCmd.Flags()
	f.BoolVar(&runExperimental, "experimental", false, "enable experimental agent features")

	// Prompt, model and session selection.
	f.StringVar(&runOpts.Prompt, "prompt", "", "prompt text (or use - to read from stdin)")
	f.StringVar(&runOpts.PromptFile, "prompt-file", "", "read the prompt from this file")
	f.StringVar(&runOpts.HistoryFile, "history-file", "", "seed the conversation from a saved history JSON file")
	f.StringVarP(&runOpts.Model, "model", "m", "", "model name")
	f.StringVar(&runOpts.Provider, "provider", "", "provider override (anthropic|openai|google|zai)")
	f.StringVar(&runOpts.Models, "models", "", "comma-separated model rotation for retries")
	f.StringVar(&runOpts.FallbackAny, "fallback-model", "", "model to fall back to when the primary fails")
	f.StringVar(&runOpts.Thinking, "thinking", "high", "reasoning effort")
	f.StringVar(&runOpts.Session, "session", "", "session ID to create/load/resume")
	f.StringVar(&runOpts.SessionDir, "session-dir", "", "directory holding persisted sessions")
	f.StringVar(&runOpts.ForkFrom, "fork-from", "", "fork a source session into --session")
	f.StringVar(&runOpts.Cwd, "cwd", ".", "working directory")
	f.StringVar(&runOpts.Profile, "profile", "", "named profile from settings to apply")
	f.StringVar(&runOpts.CacheKey, "cache-key", "", "prompt-cache partition key")

	// Output.
	f.BoolVar(&runOpts.ResultJSON, "result-json", false, "emit final result as JSON")
	f.BoolVar(&runOpts.UsageJSON, "usage-json", false, "emit cumulative usage as JSON")
	f.StringVar(&runOpts.OutputSchema, "output-schema", "", "JSON schema file the final answer must satisfy")
	f.StringVar(&runOpts.OutputSchemaName, "output-schema-name", "", "name for the output schema")
	f.BoolVar(&runOpts.TurnRecap, "turn-recap", false, "print a one-line recap after each turn")

	// Prompt shaping and discovery.
	f.StringVar(&runOpts.SystemPrompt, "system-prompt", "", "replace the built-in system prompt")
	f.StringVar(&runOpts.AppendSystemPrompt, "append-system-prompt", "", "append text to the system prompt")
	f.StringVar(&runOpts.SkillsDir, "skills", "", "comma-separated extra skill directories")
	f.BoolVar(&runOpts.SkillsProjectOnly, "skills-project-only", false, "ignore user-level skills, load project skills only")
	f.StringVar(&runOpts.Configs, "config", "", "comma-separated extra config/context files")
	f.StringVar(&runOpts.Extensions, "extension", "", "comma-separated extension directories")
	f.BoolVar(&runOpts.NoExtensions, "no-extensions", false, "disable extension discovery")
	f.StringVar(&runOpts.Hooks, "hook", "", "comma-separated hook specs")
	f.StringVar(&runOpts.Personality, "personality", "", "personality preset")
	f.StringVar(&runOpts.OutputStyle, "output-style", "", "output style preset")
	f.BoolVar(&runOpts.Advisor, "advisor", false, "enable the advisor pass")

	// Tools, MCP and containment.
	f.StringVar(&runOpts.Tools, "tools", "", "comma-separated allowlist of tools")
	f.BoolVar(&runOpts.NoTools, "no-tools", false, "run with no tools at all")
	f.BoolVar(&runOpts.NoLSP, "no-lsp", false, "disable language-server tools")
	f.BoolVar(&runOpts.NoSkills, "no-skills", false, "disable skill discovery")
	f.BoolVar(&runOpts.NoRules, "no-rules", false, "disable rule discovery")
	f.BoolVar(&runOpts.NoMCP, "no-mcp", false, "disable MCP discovery")
	f.StringVar(&runOpts.McpConfig, "mcp-config", "", "explicit MCP config path")
	f.StringVar(&runOpts.SettingsPath, "settings", "", "explicit settings.json for hooks")
	f.StringVar(&runOpts.PermissionMode, "permission-mode", "", "permission mode (auto|ask|deny|allow)")
	f.StringArrayVar(&runOpts.PermissionRules, "permission-rule", nil, "permission rule, repeatable")
	f.StringArrayVar(&runOpts.AddDirs, "add-dir", nil, "additional writable directory, repeatable")
	f.StringVar(&runOpts.Sandbox, "sandbox", "", "sandbox level (workspace|restricted-tools|process)")
	f.StringVar(&runOpts.SandboxExecutable, "sandbox-exec", "", "binary to re-exec as the process-sandbox child (defaults to this binary)")
	f.BoolVar(&runOpts.SafeMode, "safe-mode", false, "refuse destructive operations")
	f.BoolVar(&runOpts.AllowHome, "allow-home", false, "allow writes under $HOME outside the workspace")
	f.StringVar(&runOpts.Destructive, "destructive", "", "destructive-command policy (true|false)")
	f.StringVar(&runOpts.EgressDefault, "egress-default", "", "default network egress policy (allow|deny)")
	f.StringVar(&runOpts.EgressAllow, "egress-allow", "", "comma-separated egress allowlist")
	f.StringVar(&runOpts.EgressDeny, "egress-deny", "", "comma-separated egress denylist")

	// Budgets and behaviour toggles.
	f.IntVar(&runOpts.MaxTurns, "max-turns", 25, "maximum model/tool turns")
	f.IntVar(&runOpts.MaxTokenBudget, "max-token-budget", 0, "maximum total billed tokens")
	f.IntVar(&runOpts.MaxOutputTokens, "max-output-tokens", 0, "maximum output tokens per response")
	f.IntVar(&runOpts.MaxTime, "max-time", 0, "wall-clock deadline in seconds")
	f.StringVar(&runOpts.SubagentSubscription, "subagent-subscription", "", "subagent event subscription (true|false)")
	f.StringVar(&runOpts.RtkRewrite, "rtk-rewrite", "", "rewrite shell commands through rtk (true|false)")
	f.StringVar(&runOpts.ReflexCapture, "reflex-capture", "", "capture reflex traces (true|false)")
	f.BoolVar(&runOpts.NoAuthCheck, "no-auth-check", false, "skip the provider auth preflight")

	rootCmd.AddCommand(runCmd)
}
