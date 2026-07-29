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
	chatExperimental bool
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
inside the TUI to authenticate with an AI provider (Anthropic, OpenAI, etc.).`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if chatExperimental {
			agent.Enable()
		}
		if err := agent.CheckEnabled(); err != nil {
			render.PrintError(os.Stderr, false, err.Error(), "", 1)
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		opts := agent.ChatOptions{
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
		}

		return agent.RunChat(ctx, opts)
	},
}

func init() {
	chatCmd.Flags().BoolVar(&chatExperimental, "experimental", false, "enable experimental agent features")
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

	rootCmd.AddCommand(chatCmd)
}
