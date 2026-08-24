package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Facets-cloud/praxis-cli/internal/agent"
	"github.com/spf13/cobra"
)

var (
	chatExperimental bool
	chatAgents       bool
	chatModel        string
	chatThinking     string
	chatPermission   string
	chatCwd          string
	chatSessionID    string
	chatSessionDir   string
	chatResume       string
	chatTeamName     string
	chatSafe         bool
	chatEphemeral    bool
	chatMcpConfig    string
	chatSettings     string
	chatProfile      string
	chatFallback     string
	chatPrompt       string
	chatMaxTurns     int
	chatExtensions   []string
	chatGoProviders  []string
	chatAddDirs      []string
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start the interactive Praxis coding agent (experimental)",
	Long: `Launch a full-featured terminal coding agent — multi-turn conversations,
tool execution, MCP integration, session persistence, and streaming model output.

This is an EXPERIMENTAL feature. Enable it with --experimental or
PRAXIS_EXPERIMENTAL=1. The agent runs in-process (no separate binary needed)
and shares the Praxis profile directory (~/.praxis/agent/).

Authentication: the agent uses LLM provider credentials stored in
~/.praxis/agent/auth.json, separate from your Praxis control-plane credentials
in ~/.praxis/credentials. Run 'praxis chat' once and use the /login command
inside the TUI to authenticate with an AI provider (Anthropic, OpenAI, etc.).

Start views: 'praxis chat' opens a single session; 'praxis chat --agents' opens
the session dashboard, which lists persisted sessions grouped by state and
creates or resumes them from its composer. (Note 'praxis agents' is unrelated:
it lists the agent files praxis installed into your AI hosts.)

Anything after -- is passed to the agent runtime verbatim, so runtime flags this
CLI does not model stay reachable:

  praxis chat --experimental -- -no-lsp`,
	SilenceUsage:  true,
	SilenceErrors: true,
	Args:          agentPassthroughArgs("praxis chat", "pass the opening prompt with --prompt"),
	RunE: func(cmd *cobra.Command, args []string) error {
		if chatExperimental {
			agent.Enable()
		}
		// Returned, not printed here: Execute() renders it once. Printing and
		// returning would show the same gate message to the user twice.
		if err := agent.CheckEnabled(); err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		opts := agent.ChatOptions{
			AgentsView:     chatAgents,
			Model:          chatModel,
			Thinking:       chatThinking,
			PermissionMode: chatPermission,
			Cwd:            chatCwd,
			SessionID:      chatSessionID,
			SessionDir:     chatSessionDir,
			Resume:         chatResume,
			TeamName:       chatTeamName,
			SafeMode:       chatSafe,
			Ephemeral:      chatEphemeral,
			McpConfig:      chatMcpConfig,
			SettingsPath:   chatSettings,
			Profile:        chatProfile,
			FallbackModels: chatFallback,
			Prompt:         chatPrompt,
			MaxTurns:       chatMaxTurns,
			Extensions:     chatExtensions,
			GoProviders:    chatGoProviders,
			AddDirs:        chatAddDirs,
		}
		opts.ExtraArgs = args

		return agent.RunChat(ctx, opts)
	},
}

func init() {
	chatCmd.Flags().BoolVar(&chatExperimental, "experimental", false, "enable experimental agent features")
	chatCmd.Flags().BoolVar(&chatAgents, "agents", false, "start on the session dashboard instead of a single session")
	chatCmd.Flags().StringVarP(&chatModel, "model", "m", "", "model fuzzy-match (opus, gpt-5.2, glm-5.2)")
	chatCmd.Flags().StringVar(&chatThinking, "thinking", "high", "reasoning effort: off|minimal|low|medium|high|xhigh|max|ultra")
	chatCmd.Flags().StringVar(&chatPermission, "permission-mode", "auto", "permission mode: ask|auto|yolo")
	chatCmd.Flags().StringVar(&chatCwd, "cwd", ".", "working directory")
	chatCmd.Flags().StringVar(&chatSessionID, "session-id", "", "fixed session ID to create or resume")
	chatCmd.Flags().StringVar(&chatSessionDir, "session-dir", "", "directory for persisted sessions")
	chatCmd.Flags().StringVar(&chatResume, "resume", "", "resume a session by its file path")
	chatCmd.Flags().StringVar(&chatTeamName, "team-name", "", "resume an existing durable agent team")
	chatCmd.Flags().BoolVar(&chatSafe, "safe", false, "safe mode: ask permissions, block destructive ops")
	chatCmd.Flags().BoolVar(&chatEphemeral, "ephemeral", false, "do not persist the session (no resume)")
	chatCmd.Flags().StringVar(&chatMcpConfig, "mcp-config", "", "explicit MCP config path")
	chatCmd.Flags().StringVar(&chatSettings, "settings", "", "explicit settings.json for hooks")
	chatCmd.Flags().StringVar(&chatProfile, "profile", "", "isolated Praxis profile")
	chatCmd.Flags().StringVar(&chatFallback, "fallback-models", "", "comma-separated fallback model IDs")
	chatCmd.Flags().StringVar(&chatPrompt, "prompt", "", "initial prompt to send after startup")
	chatCmd.Flags().IntVar(&chatMaxTurns, "max-turns", 0, "max turns per prompt (0 = default)")
	// Repeatable, not comma-separated: a directory name may contain a comma, and
	// splitting on it would hand the agent two roots that exist nowhere.
	chatCmd.Flags().StringArrayVar(&chatExtensions, "extension", nil, "extension directory to load, repeatable")
	chatCmd.Flags().StringArrayVar(&chatGoProviders, "go-provider", nil, "Go provider plugin to load, repeatable")
	chatCmd.Flags().StringArrayVar(&chatAddDirs, "add-dir", nil, "additional writable directory, repeatable")

	// The dashboard owns session identity: it picks the row to open and clears the
	// startup prompt and resume path (harness tui.loadDashboardApplication). Refuse
	// the combinations it would silently drop rather than ignoring the user's input.
	chatCmd.MarkFlagsMutuallyExclusive("agents", "prompt")
	chatCmd.MarkFlagsMutuallyExclusive("agents", "resume")
	chatCmd.MarkFlagsMutuallyExclusive("agents", "session-id")

	rootCmd.AddCommand(chatCmd)
}
