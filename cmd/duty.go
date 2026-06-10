package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/agentcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/duties"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

// `praxis duty` surfaces Praxis Agent Schedules ("duties") — the
// unattended cron agents that emit findings and report artifacts — to
// the AI host driving the CLI. Read-only: list/runs/run/report/findings.
// Output is JSON when stdout is a pipe (the common case — an AI host
// spawns praxis as a subprocess) and pretty text in an interactive
// terminal; --json forces JSON.
//
// Duties hang off a custom agent. The default agent is the global
// "praxis" duty agent; --agent overrides with another agent name or id.

const defaultDutyAgent = "praxis"

var (
	dutyJSON  bool
	dutyAgent string

	dutyRunsDuty  string
	dutyRunsLimit int

	dutyFindingsStatus string
	dutyFindingsLimit  int
)

func init() {
	dutyCmd.PersistentFlags().BoolVar(&dutyJSON, "json", false, "JSON output (default when stdout is non-TTY)")
	dutyCmd.PersistentFlags().StringVar(&dutyAgent, "agent", defaultDutyAgent, "agent that owns the duties (name or id; defaults to the global praxis agent)")

	dutyRunsCmd.Flags().StringVar(&dutyRunsDuty, "duty", "", "filter to one duty (name or id); omit for all of the agent's runs")
	dutyRunsCmd.Flags().IntVar(&dutyRunsLimit, "limit", 20, "max runs to return (1-100), newest first")

	dutyFindingsCmd.Flags().StringVar(&dutyFindingsStatus, "status", "open", "open|resolved|all")
	dutyFindingsCmd.Flags().IntVar(&dutyFindingsLimit, "limit", 200, "max findings to return (1-1000)")

	dutyCmd.AddCommand(dutyListCmd)
	dutyCmd.AddCommand(dutyRunsCmd)
	dutyCmd.AddCommand(dutyRunCmd)
	dutyCmd.AddCommand(dutyReportCmd)
	dutyCmd.AddCommand(dutyFindingsCmd)
	rootCmd.AddCommand(dutyCmd)
}

var dutyCmd = &cobra.Command{
	Use:   "duty",
	Short: "Query Praxis Agent Schedule (duty) runs, findings, and reports (read-only)",
	Long: `Duties are scheduled agents that run unattended on a cron and emit
findings (deduped recurring issues) and artifacts (reports). This command
lets your AI host triage what they did. Read-only.

  praxis duty list                      every duty under the agent
  praxis duty runs --duty <name|id>     recent runs (newest first)
  praxis duty run <run_id>              one run's detail
  praxis duty report <run_id>           the report a run produced
  praxis duty findings <duty>           a duty's findings (default: open)

Duties hang off a custom agent; --agent defaults to the global "praxis"
agent. Output auto-emits JSON when stdout is not a TTY.`,
}

// --- agent / duty resolution ------------------------------------------

// resolveNameOrID maps a user-supplied arg to a server-side id: an exact
// name match wins first, then an exact id match, else the literal is
// passed through as a raw id (the server stays the source of truth). key
// extracts (name, id) from a list item. Shared by the agent and schedule
// resolvers so the resolution policy lives in one place.
func resolveNameOrID[T any](arg string, items []T, key func(T) (name, id string)) string {
	for _, it := range items {
		if name, _ := key(it); name == arg {
			_, id := key(it)
			return id
		}
	}
	for _, it := range items {
		if _, id := key(it); id == arg {
			return id
		}
	}
	return arg
}

// resolveAgentID maps --agent (a name or id) to the agent's server-side
// id, which addresses its nested duty resources. GLOBAL agents (the
// praxis duty agent) are fetched via include_global=true.
func resolveAgentID(out io.Writer, active credentials.Active, agentArg string) string {
	agents, err := agentcatalog.FetchIncludingGlobal(active.Profile.URL, active.Profile.Token)
	if err != nil {
		return reportResolveErr(out, active.Name, err)
	}
	return resolveNameOrID(agentArg, agents, func(a agentcatalog.Agent) (string, string) {
		return a.Name, a.ID
	})
}

// resolveScheduleID maps a <duty> arg (a name or id) to a schedule id
// under the resolved agent. Same name→id-then-passthrough policy as
// resolveAgentID.
func resolveScheduleID(out io.Writer, active credentials.Active, agentID, dutyArg string) string {
	schedules, err := duties.ListSchedules(active.Profile.URL, active.Profile.Token, agentID, "")
	if err != nil {
		return reportResolveErr(out, active.Name, err)
	}
	return resolveNameOrID(dutyArg, schedules, func(s duties.Schedule) (string, string) {
		return s.Name, s.ID
	})
}

// reportResolveErr funnels resolution-time transport errors through the
// shared HTTP-error dispatcher. Declared as a thin wrapper (rather than
// calling reportHTTPErr inline) only so the helpers above read cleanly;
// reportHTTPErr exits the process, so the string return is unreachable.
func reportResolveErr(out io.Writer, profile string, err error) string {
	_ = reportHTTPErr(out, profile, err)
	return ""
}

// --- list -------------------------------------------------------------

var dutyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the duties (schedules) under the agent",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(dutyJSON, false, out)
		active := activeOrAuthExit(out)
		agentID := resolveAgentID(out, active, dutyAgent)

		schedules, err := duties.ListSchedules(active.Profile.URL, active.Profile.Token, agentID, "")
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if schedules == nil {
			schedules = []duties.Schedule{}
		}
		if asJSON {
			return render.JSON(out, schedules)
		}
		printSchedulesPretty(out, dutyAgent, agentID, schedules)
		return nil
	},
}

// --- runs -------------------------------------------------------------

var dutyRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List recent runs for a duty (or all of the agent's runs)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		if dutyRunsLimit < 1 || dutyRunsLimit > 100 {
			usageExit(out, "--limit must be between 1 and 100", "")
		}
		asJSON := render.UseJSON(dutyJSON, false, out)
		active := activeOrAuthExit(out)
		agentID := resolveAgentID(out, active, dutyAgent)

		scheduleID := ""
		if dutyRunsDuty != "" {
			scheduleID = resolveScheduleID(out, active, agentID, dutyRunsDuty)
		}

		runs, err := duties.ListRuns(active.Profile.URL, active.Profile.Token, agentID, scheduleID, dutyRunsLimit)
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if runs == nil {
			runs = []duties.Run{}
		}
		if asJSON {
			return render.JSON(out, runs)
		}
		printRunsPretty(out, dutyAgent, agentID, dutyRunsDuty, runs)
		return nil
	},
}

// --- run --------------------------------------------------------------

var dutyRunCmd = &cobra.Command{
	Use:   "run <run_id>",
	Short: "Show one run's detail (status, findings, report_artifact_id)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(dutyJSON, false, out)
		active := activeOrAuthExit(out)
		agentID := resolveAgentID(out, active, dutyAgent)

		run, err := duties.GetRun(active.Profile.URL, active.Profile.Token, agentID, args[0])
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if asJSON {
			return render.JSON(out, run)
		}
		printRunPretty(out, run)
		return nil
	},
}

// --- report -----------------------------------------------------------

var dutyReportCmd = &cobra.Command{
	Use:   "report <run_id>",
	Short: "Print the report artifact a run produced",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(dutyJSON, false, out)
		active := activeOrAuthExit(out)
		agentID := resolveAgentID(out, active, dutyAgent)

		run, err := duties.GetRun(active.Profile.URL, active.Profile.Token, agentID, args[0])
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if run.ReportArtifactID == nil || *run.ReportArtifactID == "" {
			render.PrintError(out, asJSON,
				fmt.Sprintf("run %q produced no report artifact", args[0]),
				"the run may still be in progress or emitted only findings",
				exitcode.Error)
			os.Exit(exitcode.Error)
		}

		body, mime, err := duties.FetchArtifactContent(active.Profile.URL, active.Profile.Token, *run.ReportArtifactID)
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if asJSON {
			return render.JSON(out, map[string]string{
				"artifact_id": *run.ReportArtifactID,
				"mime":        mime,
				"content":     string(body),
			})
		}
		_, _ = out.Write(body)
		if len(body) > 0 && body[len(body)-1] != '\n' {
			fmt.Fprintln(out)
		}
		return nil
	},
}

// --- findings ---------------------------------------------------------

var dutyFindingsCmd = &cobra.Command{
	Use:   "findings <duty>",
	Short: "List a duty's findings (deduped by finding_key)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		switch dutyFindingsStatus {
		case "open", "resolved", "all":
		default:
			usageExit(out, fmt.Sprintf("invalid --status %q", dutyFindingsStatus), "allowed: open | resolved | all")
		}
		if dutyFindingsLimit < 1 || dutyFindingsLimit > 1000 {
			usageExit(out, "--limit must be between 1 and 1000", "")
		}
		asJSON := render.UseJSON(dutyJSON, false, out)
		active := activeOrAuthExit(out)
		agentID := resolveAgentID(out, active, dutyAgent)
		scheduleID := resolveScheduleID(out, active, agentID, args[0])

		findings, err := duties.ListFindings(active.Profile.URL, active.Profile.Token, agentID, scheduleID, dutyFindingsStatus, dutyFindingsLimit)
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if findings == nil {
			findings = []duties.Finding{}
		}
		if asJSON {
			return render.JSON(out, findings)
		}
		printFindingsPretty(out, findings)
		return nil
	},
}

// agentLabel formats the agent for human messages: the name/id the user
// passed, plus the resolved id in brackets when resolution changed it
// (so an empty list/runs result makes clear *which* agent was actually
// queried). Used by both the schedules and runs empty-state messages.
func agentLabel(agentArg, agentID string) string {
	if agentID != "" && agentID != agentArg {
		return fmt.Sprintf("%q [%s]", agentArg, agentID)
	}
	return fmt.Sprintf("%q", agentArg)
}

// --- pretty printers --------------------------------------------------

func printSchedulesPretty(out io.Writer, agentArg, agentID string, schedules []duties.Schedule) {
	if len(schedules) == 0 {
		fmt.Fprintf(out, "(no duties for agent %s)\n", agentLabel(agentArg, agentID))
		return
	}
	for _, s := range schedules {
		state := "enabled"
		if !s.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(out, "%s  (%s)\n", s.Name, s.ID)
		fmt.Fprintf(out, "  %s · %s · %s · open findings: %d\n",
			s.DisplayName, s.Status, state, s.OpenFindingsCount)
		fmt.Fprintf(out, "  cron: %s %s\n", s.CronExpression, s.Timezone)
		if s.LastRunAt != nil {
			fmt.Fprintf(out, "  last run: %s\n", *s.LastRunAt)
		}
		fmt.Fprintln(out)
	}
}

func printRunsPretty(out io.Writer, agentArg, agentID, dutyArg string, runs []duties.Run) {
	if len(runs) == 0 {
		if dutyArg != "" {
			fmt.Fprintf(out, "(no runs for duty %q under agent %s)\n", dutyArg, agentLabel(agentArg, agentID))
		} else {
			fmt.Fprintf(out, "(no runs for agent %s)\n", agentLabel(agentArg, agentID))
		}
		return
	}
	for _, r := range runs {
		fmt.Fprintf(out, "%s  %s  started %s\n", r.ID, r.Status, r.StartedAt)
		fmt.Fprintf(out, "  findings: %d  actions: %d", len(r.Findings), len(r.Actions))
		if r.ReportArtifactID != nil && *r.ReportArtifactID != "" {
			fmt.Fprintf(out, "  report: yes")
		}
		fmt.Fprintln(out)
	}
}

func printRunPretty(out io.Writer, r *duties.Run) {
	fmt.Fprintf(out, "run %s\n", r.ID)
	fmt.Fprintf(out, "  status: %s\n", r.Status)
	fmt.Fprintf(out, "  schedule: %s\n", r.ScheduleID)
	fmt.Fprintf(out, "  started: %s\n", r.StartedAt)
	if r.CompletedAt != nil {
		fmt.Fprintf(out, "  completed: %s\n", *r.CompletedAt)
	}
	if r.ErrorMessage != nil && *r.ErrorMessage != "" {
		fmt.Fprintf(out, "  error: %s\n", *r.ErrorMessage)
	}
	if r.ReportArtifactID != nil && *r.ReportArtifactID != "" {
		fmt.Fprintf(out, "  report artifact: %s  (praxis duty report %s)\n", *r.ReportArtifactID, r.ID)
	}
	if len(r.Findings) > 0 {
		fmt.Fprintf(out, "  findings (%d):\n", len(r.Findings))
		for _, f := range r.Findings {
			fmt.Fprintf(out, "    [%s] %s\n", f.Severity, f.Title)
		}
	}
}

func printFindingsPretty(out io.Writer, findings []duties.Finding) {
	if len(findings) == 0 {
		fmt.Fprintln(out, "(no findings)")
		return
	}
	for _, f := range findings {
		key := ""
		if f.FindingKey != nil {
			key = *f.FindingKey
		}
		fmt.Fprintf(out, "[%s] %s  (%s)\n", f.Severity, f.Title, f.Status)
		fmt.Fprintf(out, "  key: %s  seen x%d\n", key, f.RecurrenceCount)
		if f.Service != nil && *f.Service != "" {
			fmt.Fprintf(out, "  service: %s\n", *f.Service)
		}
		if f.Environment != nil && *f.Environment != "" {
			fmt.Fprintf(out, "  environment: %s\n", *f.Environment)
		}
		if strings.TrimSpace(f.Description) != "" {
			fmt.Fprintf(out, "  %s\n", f.Description)
		}
		fmt.Fprintln(out)
	}
}
