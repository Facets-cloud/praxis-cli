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
	Short: "List subagents installed by praxis across detected AI hosts",
	Long: `List every Praxis-sourced subagent file currently installed on this
machine. Reads ~/.praxis/installed.json — no network call. The two
kinds:

  agent     — sourced from /ai-api/custom-agents
  subagent  — sourced from /ai-api/subagents (org-wide or global only;
              agent-specific subagents are filtered out at fetch time)

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
		return printAgentsPretty(out, shaped)
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

func printAgentsPretty(out io.Writer, entries []agentEntryForOutput) error {
	fmt.Fprintf(out, "%-32s  %-9s  %-12s  %s\n", "NAME", "KIND", "HARNESS", "PATH")
	fmt.Fprintln(out, "─────────────────────────────────────────────────────────────────────────────")
	for _, e := range entries {
		fmt.Fprintf(out, "%-32s  %-9s  %-12s  %s\n", e.AgentName, e.Kind, e.Harness, e.Path)
	}
	return nil
}
