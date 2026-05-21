package cmd

import (
	"fmt"
	"io"

	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

var agentsJSON bool

func init() {
	agentsCmd.Flags().BoolVar(&agentsJSON, "json", false,
		"JSON output (default when stdout is non-TTY)")
	rootCmd.AddCommand(agentsCmd)
}

var agentsCmd = &cobra.Command{
	Use:   "agents",
	Short: "List agents installed by praxis across detected AI hosts",
	Long: `List every Praxis-sourced agent file currently installed on this
machine. Reads ~/.praxis/installed.json — no network call. Agents are
sourced from /ai-api/custom-agents by ` + "`praxis login`" + ` and installed
into the supported hosts' subagent directories:

  Claude Code → ~/.claude/agents/praxis-<n>.md
  Gemini CLI  → ~/.gemini/agents/praxis-<n>.md

Codex is gated off for agents in v1 — the TOML render path is
implemented but its runtime didn't surface the files in smoke
testing; flipping ` + "`supportsAgentInstall`" + ` re-enables it.

Re-run ` + "`praxis login`" + ` to refresh the on-disk set against the active
profile.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(agentsJSON, false, out)

		entries, err := listInstalledAgents()
		if err != nil {
			return err
		}
		shaped := toAgentOutputShape(entries)

		if asJSON {
			// Always emit `[]` (never `null`) so AI host JSON parsers
			// don't have to handle two empty shapes.
			if shaped == nil {
				shaped = []agentEntryForOutput{}
			}
			return render.JSON(out, shaped)
		}

		if len(shaped) == 0 {
			fmt.Fprintln(out, "No agents installed. Try `praxis login`.")
			return nil
		}
		printAgentsPretty(out, shaped)
		return nil
	},
}

type agentEntryForOutput struct {
	AgentName string `json:"agent_name"`
	Kind      string `json:"kind"`
	Harness   string `json:"harness"`
	Path      string `json:"path"`
}

func toAgentOutputShape(entries []skillinstall.AgentInstallation) []agentEntryForOutput {
	out := make([]agentEntryForOutput, 0, len(entries))
	for _, e := range entries {
		out = append(out, agentEntryForOutput{
			AgentName: e.AgentName,
			Kind:      e.Kind,
			Harness:   e.Harness,
			Path:      e.Path,
		})
	}
	return out
}

// printAgentsPretty writes the table-formatted listing to out. Returns
// no error — fmt.Fprintf/Fprintln write failures (e.g. a pipe closing
// mid-stream) aren't actionable here; the CLI surface treats `agents`
// as best-effort terminal output, identical to `list-skills`.
func printAgentsPretty(out io.Writer, entries []agentEntryForOutput) {
	fmt.Fprintf(out, "%-32s  %-9s  %-12s  %s\n", "NAME", "KIND", "HARNESS", "PATH")
	fmt.Fprintln(out, "─────────────────────────────────────────────────────────────────────────────")
	for _, e := range entries {
		fmt.Fprintf(out, "%-32s  %-9s  %-12s  %s\n", e.AgentName, e.Kind, e.Harness, e.Path)
	}
}
