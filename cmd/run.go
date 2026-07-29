package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Facets-cloud/praxis-cli/internal/agent"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	runExperimental   bool
	runPrompt         string
	runPromptFile     string
	runModel          string
	runProvider       string
	runThinking       string
	runSession        string
	runForkFrom       string
	runCwd            string
	runResultJSON     bool
	runUsageJSON      bool
	runNoMCP          bool
	runMcpConfig      string
	runSettings       string
	runMaxTurns       int
	runMaxTokenBudget int
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

Examples:
  praxis run --experimental --prompt "fix the failing test"
  praxis run --experimental --prompt - < input.txt
  praxis run --experimental --prompt-file task.txt --model opus
  praxis run --experimental --prompt "refactor" --result-json`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if runExperimental {
			agent.Enable()
		}
		if err := agent.CheckEnabled(); err != nil {
			render.PrintError(os.Stderr, false, err.Error(), "", 1)
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		ha := agent.HeadlessArgs{
			Prompt:         runPrompt,
			PromptFile:     runPromptFile,
			Model:          runModel,
			Provider:       runProvider,
			Thinking:       runThinking,
			Session:        runSession,
			ForkFrom:       runForkFrom,
			Cwd:            runCwd,
			ResultJSON:     runResultJSON,
			UsageJSON:      runUsageJSON,
			NoMCP:          runNoMCP,
			McpConfig:      runMcpConfig,
			SettingsPath:   runSettings,
			MaxTurns:       runMaxTurns,
			MaxTokenBudget: runMaxTokenBudget,
		}

		exitCode := agent.RunHeadless(ctx, ha.ToNativeArgs())
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	},
}

func init() {
	runCmd.Flags().BoolVar(&runExperimental, "experimental", false, "enable experimental agent features")
	runCmd.Flags().StringVar(&runPrompt, "prompt", "", "prompt text (or use - to read from stdin)")
	runCmd.Flags().StringVar(&runPromptFile, "prompt-file", "", "read the prompt from this file")
	runCmd.Flags().StringVarP(&runModel, "model", "m", "", "model name")
	runCmd.Flags().StringVar(&runProvider, "provider", "", "provider override (anthropic|openai|google|zai)")
	runCmd.Flags().StringVar(&runThinking, "thinking", "high", "reasoning effort")
	runCmd.Flags().StringVar(&runSession, "session", "", "session ID to create/load/resume")
	runCmd.Flags().StringVar(&runForkFrom, "fork-from", "", "fork a source session into -session")
	runCmd.Flags().StringVar(&runCwd, "cwd", ".", "working directory")
	runCmd.Flags().BoolVar(&runResultJSON, "result-json", false, "emit final result as JSON")
	runCmd.Flags().BoolVar(&runUsageJSON, "usage-json", false, "emit cumulative usage as JSON")
	runCmd.Flags().BoolVar(&runNoMCP, "no-mcp", false, "disable MCP discovery")
	runCmd.Flags().StringVar(&runMcpConfig, "mcp-config", "", "explicit MCP config path")
	runCmd.Flags().StringVar(&runSettings, "settings", "", "explicit settings.json for hooks")
	runCmd.Flags().IntVar(&runMaxTurns, "max-turns", 25, "maximum model/tool turns")
	runCmd.Flags().IntVar(&runMaxTokenBudget, "max-token-budget", 0, "maximum total billed tokens")

	rootCmd.AddCommand(runCmd)
}
