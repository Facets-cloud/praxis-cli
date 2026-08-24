package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Facets-cloud/praxis-cli/internal/agent"
	"github.com/spf13/cobra"
)

// Seams: tests swap these to assert the argv this CLI hands the agent runtime
// without launching a model or spawning MCP children.
var (
	agentRunNative = agent.RunNative
	agentRunSkills = func(ctx context.Context, args []string) int {
		return agent.RunSkillsReport(ctx, args, os.Stdout, os.Stderr)
	}
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage the in-process Praxis coding agent (experimental)",
	Long: `Manage the coding agent that "praxis chat" and "praxis run" execute
in-process: its plugins, its local MCP servers, its Slack persona, its persisted
sessions, and its skill usage.

Not to be confused with "praxis agents" (plural), which lists the custom cloud
agents praxis installed into your local AI hosts. This command group configures
the local agent runtime; that one reports on installed agent definitions.

These are EXPERIMENTAL features. Enable them with --experimental or
PRAXIS_EXPERIMENTAL=1.

Each subcommand forwards its arguments to the agent runtime verbatim, so the
runtime's own flags work unchanged and cannot drift out of sync with this CLI.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

// agentPassthrough builds the RunE for a subcommand that forwards its arguments
// to the agent runtime. prefix is the runtime's own selector for the capability
// ("plugin", "mcp", "-acp", …).
//
// Flag parsing is disabled on these commands so runtime flags reach the runtime
// intact; that means --experimental and --help have to be recognized here.
func agentPassthrough(run func(context.Context, []string) int, prefix ...string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		forward := make([]string, 0, len(prefix)+len(args))
		forward = append(forward, prefix...)
		for _, arg := range args {
			switch arg {
			case "-h", "--help", "help":
				return cmd.Help()
			case "--experimental", "-experimental":
				agent.Enable()
			default:
				forward = append(forward, arg)
			}
		}

		// Returned, not printed here: Execute() renders it once.
		if err := agent.CheckEnabled(); err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		if code := run(ctx, forward); code != 0 {
			os.Exit(code)
		}
		return nil
	}
}

// agentPassthroughArgs validates the positionals of a flag-parsing command whose
// leftovers are forwarded to the agent runtime. Everything the user puts after --
// is forwarded, so the only thing to reject is a bare word: that is a prompt or a
// subcommand typed without its flag, and forwarding it would run something other
// than what was typed.
//
// It deliberately does not consult cobra's ArgsLenAtDash: that value is recorded
// on the shared flag set and survives into the next parse, so a command invoked
// twice in one process would judge the second call by the first call's dashes.
func agentPassthroughArgs(command, hint string) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			return fmt.Errorf("unexpected argument %q: %s, and agent flags after -- (see %s --help)", args[0], hint, command)
		}
		return nil
	}
}

func newAgentPassthroughCmd(use, short, long string, run func(context.Context, []string) int, prefix ...string) *cobra.Command {
	return &cobra.Command{
		Use:                use,
		Short:              short,
		Long:               long,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		SilenceErrors:      true,
		RunE:               agentPassthrough(run, prefix...),
	}
}

func init() {
	agentCmd.AddCommand(newAgentPassthroughCmd(
		"plugin [command]",
		"Install and manage agent plugins",
		`Manage plugin marketplaces and the plugins installed from them.

Commands:
  marketplace add <source>             add a marketplace (owner/repo, git/JSON URL, local path)
  marketplace list                     list added marketplaces
  marketplace update [name]            refresh one or all marketplaces
  marketplace remove <name>            remove a marketplace
  install <id> [--scope user|project]  install name@marketplace
  update <id> [--scope ...]            update an installed plugin
  uninstall <id> [--scope ...]         uninstall an installed plugin
  enable <id> [--scope ...]            enable an installed plugin
  disable <id> [--scope ...]           disable an installed plugin
  list                                 list plugins visible in the current project

Examples:
  praxis agent plugin --experimental marketplace add Facets-cloud/praxis-plugins
  praxis agent plugin --experimental install reviewer@praxis-plugins --scope project
  praxis agent plugin --experimental list`,
		agentRunNativeIndirect, "plugin"))

	agentCmd.AddCommand(newAgentPassthroughCmd(
		"mcp [command]",
		"Configure the agent's local MCP servers",
		`Manage the MCP servers the local agent connects to, stored in
~/.praxis/agent/mcp.json (or ./.praxis/mcp.json for a project).

This is the LOCAL agent's server list. The separate "praxis mcp" command invokes
tool functions on your Praxis deployment's server-side MCP gateway.

Commands:
  add <name> <command-or-url> [args...]  connect and save the server
  list                                   show every MCP server the agent resolves, and from where
  import [flags]                         import servers from Claude Code, Codex and opencode
  login <name-or-url>                    authorize a remote server in a browser (OAuth)
  logout <name-or-url>                   forget a remote server's stored credential

Import flags: --from <ids>, --project, --dry-run, --overwrite, --include-disabled

Examples:
  praxis agent mcp --experimental add serena uvx serena-mcp --project .
  praxis agent mcp --experimental import --from codex --dry-run
  praxis agent mcp --experimental login linear`,
		agentRunNativeIndirect, "mcp"))

	agentCmd.AddCommand(newAgentPassthroughCmd(
		"slack [command]",
		"Connect the agent to Slack",
		`Set up, inspect, or disconnect the agent's Slack persona, so it can be
driven from a Slack workspace through the broker.

Commands:
  setup [--broker URL]     link a Slack workspace
  status                   show the current link
  disconnect [team-id]     unlink a workspace

Examples:
  praxis agent slack --experimental setup
  praxis agent slack --experimental status`,
		agentRunNativeIndirect, "slack"))

	agentCmd.AddCommand(newAgentPassthroughCmd(
		"sessions [flags]",
		"List or prune persisted agent sessions",
		`Report the agent's persisted sessions as a table, and prune the empty ones.
This is the headless view of the same dashboard "praxis chat --agents" opens.

Flags:
  -json                    emit the table as JSON
  -session-dir <path>      sessions directory to read
  -agent-dir <path>        agent state directory
  -profile <name>          isolate to a named profile
  -prune-empty             delete sessions with no messages
  -older-than <duration>   only prune sessions older than this (e.g. 48h)
  -dry-run                 report what pruning would delete, and delete nothing

Examples:
  praxis agent sessions --experimental
  praxis agent sessions --experimental -json
  praxis agent sessions --experimental -prune-empty -older-than 48h -dry-run`,
		agentRunNativeIndirect, "agents"))

	agentCmd.AddCommand(newAgentPassthroughCmd(
		"skills [flags]",
		"Report agent skill usage and idle skills",
		`Read-only report of which skills the agent actually activated, and which have
been idle long enough to deserve a review. Nothing is disabled or deleted: idle
skills are only flagged.

Flags:
  -inactive <duration>   flag skills idle at least this long (default 720h)
  -json                  emit the report as JSON
  -all                   include skills that left the catalog but have usage history
  -cwd <path>            project directory whose skills to report
  -profile <name>        isolate to a named profile

Examples:
  praxis agent skills --experimental
  praxis agent skills --experimental -inactive 168h -json`,
		agentRunSkillsIndirect))

	agentCmd.AddCommand(newAgentPassthroughCmd(
		"acp",
		"Serve the agent over the Agent Client Protocol (stdio)",
		`Run the agent as an ACP server on stdin/stdout, for editors and IDEs that
speak the Agent Client Protocol. Not meant to be run interactively: the editor
spawns it.

Example (editor config):
  command: praxis
  args: ["agent", "acp", "--experimental"]`,
		agentRunNativeIndirect, "-acp"))

	agentCmd.AddCommand(newAgentPassthroughCmd(
		"sdk",
		"Serve the agent over the JSONL SDK protocol (stdio)",
		`Run the agent as a JSONL request/response server on stdin/stdout, for
programmatic drivers that embed Praxis. Not meant to be run interactively.

Example:
  echo '{"type":"prompt","prompt":"hello"}' | praxis agent sdk --experimental`,
		agentRunNativeIndirect, "-sdk"))

	rootCmd.AddCommand(agentCmd)
}

// The indirections keep the test seams live: newAgentPassthroughCmd captures the
// runner at init time, so it must capture a function that dispatches through the
// package-level var rather than the var's value at init.
func agentRunNativeIndirect(ctx context.Context, args []string) int {
	return agentRunNative(ctx, args)
}

func agentRunSkillsIndirect(ctx context.Context, args []string) int {
	return agentRunSkills(ctx, args)
}
