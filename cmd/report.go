package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/clifeed"
	"github.com/Facets-cloud/praxis-cli/internal/httpclient"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

// reportMaxBody caps what `praxis report` reads from stdin.
// reportMaxBody matches the server's limit, so a report the server would refuse is
// refused here, with the reason, and is never queued.
const reportMaxBody = 64 << 10

// reportFlushMax and reportFlushBudget bound the flush of queued reports after
// a command.
const (
	reportFlushMax    = 3
	reportFlushBudget = 5 * time.Second
)

var (
	reportMessage string
	reportJSON    bool
)

// Seams so tests can stub the network.
var (
	sendReport  = clifeed.SendReport
	queueReport = clifeed.QueueReport
)

func init() {
	reportCmd.Flags().StringVarP(&reportMessage, "message", "m", "", "the report; stdin is read when this is empty")
	reportCmd.Flags().BoolVar(&reportJSON, "json", false, "JSON output")
	rootCmd.AddCommand(reportCmd)
}

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "File a friction report: what went wrong and how you recovered",
	Long: `Report friction to the Praxis team, without waiting for a person.

Write what happened and how you recovered; the recovery sentence is the most
valuable line. The first line becomes the title. Pass the report with -m, or
on stdin. The CLI attaches the version, the platform, the AI host, the session,
the active control plane and the last commands of raptor and praxis. Command
arguments and flag values are never included.

The report is filed in a private repository. The CLI only confirms it. If the
feed cannot be reached, the report is kept and sent with a later command.
Reports are off in CI.`,
	Args: cobra.NoArgs,
	RunE: runReport,
}

func runReport(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	asJSON := render.UseJSON(reportJSON, false, out)
	if ci := httpclient.CIName(); ci != "" {
		return fmt.Errorf("reports are off in CI (%s): a pipeline would file the same report on every run", ci)
	}
	body, err := reportBody(cmd.InOrStdin())
	if err != nil {
		return err
	}
	req := clifeed.NewReport(body)
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	res, err := sendReport(ctx, req)
	if err == nil {
		if asJSON {
			return render.JSON(out, res)
		}
		fmt.Fprintln(out, "Thank you — the report is filed.")
		return nil
	}
	var re *clifeed.ReportError
	if !errors.As(err, &re) || !re.Transient() {
		return err
	}
	if qerr := queueReport(req, "praxis"); qerr != nil {
		return errors.Join(err, fmt.Errorf("and the report could not be kept: %w", qerr))
	}
	if asJSON {
		return render.JSON(out, clifeed.ReportResult{Status: "queued"})
	}
	fmt.Fprintln(out, "The feed could not be reached. The report is kept and goes with a later praxis or raptor command.")
	return nil
}

// reportBody returns the report from -m, else from stdin. A terminal on stdin
// is not read, so the command never waits for input nobody is typing.
func reportBody(stdin io.Reader) (string, error) {
	body := strings.TrimSpace(reportMessage)
	if body == "" && !isTerminal(stdin) {
		raw, err := io.ReadAll(io.LimitReader(stdin, reportMaxBody+1))
		if err != nil {
			return "", fmt.Errorf("read the report from stdin: %w", err)
		}
		body = strings.TrimSpace(string(raw))
	}
	if body == "" {
		return "", errors.New("the report is empty: pass it with -m, or on stdin")
	}
	if len(body) > reportMaxBody {
		return "", fmt.Errorf("the report is more than %d bytes; shorten it, nothing was sent", reportMaxBody)
	}
	return body, nil
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// historySkipped lists commands that add only noise to the history: version
// output, shell completion, and the hook and git helpers that hosts call
// many times.
var historySkipped = map[string]bool{
	"version": true, "completion": true, "__complete": true, "__completeNoDesc": true,
	"hook": true, "git-credential": true,
}

// recordHistory appends this invocation to the shared history. Best-effort,
// never in CI, and never for the skipped commands.
func recordHistory(args []string, command string, started time.Time, failed bool) {
	if command == "" || httpclient.CIName() != "" || historySkipped[strings.Fields(command)[0]] {
		return
	}
	for _, a := range args {
		if a == "--version" || a == "-v" {
			return
		}
	}
	exit := 0
	if failed {
		exit = 1
	}
	_ = clifeed.AppendHistory(clifeed.HistoryEntry{
		Time:       started.UTC(),
		CLI:        "praxis",
		Version:    strings.TrimPrefix(version, "v"),
		Session:    httpclient.Session(),
		Command:    command,
		Flags:      clifeed.FlagNames(args),
		Exit:       exit,
		DurationMS: time.Since(started).Milliseconds(),
	})
}

// flushSkipped lists commands after which queued reports are not sent:
// `report` sends its own, and the rest are fast helpers or self-managing.
var flushSkipped = map[string]bool{
	"report": true, "hook": true, "git-credential": true, "__complete": true, "__completeNoDesc": true,
}

// flushQueuedReports sends a few queued reports after a command, within a
// short budget. Silent, and never in CI.
func flushQueuedReports(args []string, command string) {
	if command == "" || skipUpdateCheck(args) || flushSkipped[strings.Fields(command)[0]] || httpclient.CIName() != "" {
		return
	}
	if !clifeed.HasQueuedReports() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reportFlushBudget)
	defer cancel()
	clifeed.FlushReports(ctx, reportFlushMax)
}
