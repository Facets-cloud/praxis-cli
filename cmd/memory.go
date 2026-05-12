package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/memory"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

// The memory CLI is for AI hosts (Claude, Cursor, Gemini CLI), not
// humans. Every command emits JSON unconditionally. The --json flag
// stays as a no-op so the praxis meta-skill's convention ("always
// pass --json from a tool loop") doesn't have to special-case the
// memory subcommand. Pretty-text output is intentionally not
// implemented — human inspection of org memories happens in the
// agent-factory UI at /knowledge/memories, not at the terminal.

var (
	memoryJSON          bool // no-op; output is always JSON
	memoryRecallLimit   int
	memoryListLimit     int
	memoryListOffset    int
	memoryListCategory  string
	memoryListTagsCSV   string
	memoryAddTitle      string
	memoryAddContent    string
	memoryAddSummary    string
	memoryAddSlug       string
	memoryAddKind       string
	memoryAddAudience   string
	memoryAddImportance string
	memoryAddTagsCSV    string
)

func init() {
	memoryCmd.PersistentFlags().BoolVar(&memoryJSON, "json", true, "JSON output (always on — accepted for praxis-skill convention compat)")

	memoryRecallCmd.Flags().IntVar(&memoryRecallLimit, "limit", 5, "max matches to return (1-20)")

	memoryListCmd.Flags().StringVar(&memoryListCategory, "category", "", "filter by category")
	memoryListCmd.Flags().StringVar(&memoryListTagsCSV, "tag", "", "filter by tags (comma-separated; any-match)")
	memoryListCmd.Flags().IntVar(&memoryListLimit, "limit", 100, "max rows (server cap=100)")
	memoryListCmd.Flags().IntVar(&memoryListOffset, "offset", 0, "pagination offset — bump by --limit to walk past 100 rows")

	memoryAddCmd.Flags().StringVar(&memoryAddTitle, "title", "", "human-readable title (required)")
	memoryAddCmd.Flags().StringVar(&memoryAddContent, "content", "", "memory body (required; use - for stdin)")
	memoryAddCmd.Flags().StringVar(&memoryAddSummary, "summary", "", "one-line description")
	memoryAddCmd.Flags().StringVar(&memoryAddSlug, "slug", "", "filesystem-safe identifier (auto-derived from title when omitted)")
	memoryAddCmd.Flags().StringVar(&memoryAddKind, "kind", "user", "user|feedback|project|reference")
	memoryAddCmd.Flags().StringVar(&memoryAddAudience, "audience", "user", "user|org (agent-scoped writes belong to agent-factory, not the CLI)")
	memoryAddCmd.Flags().StringVar(&memoryAddImportance, "importance", "medium", "low|medium|high|critical")
	memoryAddCmd.Flags().StringVar(&memoryAddTagsCSV, "tag", "", "comma-separated tags")

	memoryCmd.AddCommand(memoryRecallCmd)
	memoryCmd.AddCommand(memoryListCmd)
	memoryCmd.AddCommand(memoryAddCmd)
	rootCmd.AddCommand(memoryCmd)
}

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Recall and manage org memories (AI-only — JSON output)",
	Long: `Memories are durable facts (conventions, decisions, references) the
organization has captured on this Praxis deployment. The praxis-memory
meta-skill teaches AI hosts when to read vs write.

Read paths:
  praxis memory recall "<query>"   Server-side keyword ranking. Fast,
                                   narrow, scored. Best when the user's
                                   terms overlap memory content obviously.
  praxis memory list               Full dump of every memory (content +
                                   metadata). Grep the JSON client-side
                                   when keywords are weak or recall
                                   misses the row you need.

Write path:
  praxis memory add --title ...    Persist a new memory. Default audience
                                   is "user" (caller's cell across agents);
                                   pass --audience org for org-wide.

Output is always JSON — this CLI is consumed by AI hosts, not humans.
Human inspection lives in the agent-factory UI.`,
}

// activeOrAuthExit resolves the current credentials profile or exits
// with the auth code. Mirrors cmd/mcp.go.
func activeOrAuthExit(out io.Writer) credentials.Active {
	active, err := credentials.ResolveActive("")
	if err != nil {
		render.PrintError(out, true, err.Error(), "could not load credentials", exitcode.Error)
		os.Exit(exitcode.Error)
	}
	if !active.Loaded || active.Profile.Token == "" {
		render.PrintError(out, true,
			fmt.Sprintf("no credentials for profile %q", active.Name),
			"run `praxis login` (or `praxis login --profile "+active.Name+"`)",
			exitcode.Auth)
		os.Exit(exitcode.Auth)
	}
	return active
}

// --- recall -----------------------------------------------------------

var memoryRecallCmd = &cobra.Command{
	Use:   "recall <query>",
	Short: "Search memories by relevance (server-side keyword ranking)",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		active := activeOrAuthExit(out)

		query := strings.Join(args, " ")
		results, err := memory.Recall(active.Profile.URL, active.Profile.Token, memory.RecallRequest{
			Query: query,
			Limit: memoryRecallLimit,
		})
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		// Ensure consistent empty-array output rather than `null` so the
		// AI's JSON parser doesn't have to handle two shapes.
		if results == nil {
			results = []memory.Memory{}
		}
		return render.JSON(out, results)
	},
}

// --- list -------------------------------------------------------------

var memoryListCmd = &cobra.Command{
	Use:   "list",
	Short: "Dump every memory with full content (for client-side grep)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		active := activeOrAuthExit(out)

		params := memory.ListParams{
			Category: memoryListCategory,
			Limit:    memoryListLimit,
			Offset:   memoryListOffset,
		}
		if memoryListTagsCSV != "" {
			params.Tags = splitCSV(memoryListTagsCSV)
		}

		results, err := memory.List(active.Profile.URL, active.Profile.Token, params)
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		if results == nil {
			results = []memory.Memory{}
		}
		return render.JSON(out, results)
	},
}

// --- add --------------------------------------------------------------

var memoryAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Persist a new memory to the Praxis deployment",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()

		if memoryAddTitle == "" || memoryAddContent == "" {
			render.PrintError(out, true,
				"both --title and --content are required",
				"e.g. `praxis memory add --title 'Retry budgets' --content 'every external call wraps...'`",
				exitcode.Usage)
			os.Exit(exitcode.Usage)
		}
		content := memoryAddContent
		if content == "-" {
			raw, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				render.PrintError(out, true, fmt.Sprintf("read stdin: %v", err), "", exitcode.Error)
				os.Exit(exitcode.Error)
			}
			content = string(raw)
		}

		active := activeOrAuthExit(out)

		req := memory.CreateRequest{
			Title:      memoryAddTitle,
			Slug:       memoryAddSlug,
			Content:    content,
			Summary:    memoryAddSummary,
			Kind:       memory.Kind(memoryAddKind),
			Audience:   memory.Audience(memoryAddAudience),
			Importance: memory.Importance(memoryAddImportance),
		}
		if memoryAddTagsCSV != "" {
			req.Tags = splitCSV(memoryAddTagsCSV)
		}

		m, err := memory.Create(active.Profile.URL, active.Profile.Token, req)
		if err != nil {
			return reportHTTPErr(out, active.Name, err)
		}
		return render.JSON(out, m)
	},
}

// --- helpers ----------------------------------------------------------

// reportHTTPErr maps low-level transport errors to praxis exit codes.
// Always emits the JSON {error, hint, code} envelope.
func reportHTTPErr(out io.Writer, profile string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "HTTP 401"), strings.Contains(msg, "HTTP 403"):
		render.PrintError(out, true, msg,
			"the API key may be missing or revoked; run `praxis login --profile "+profile+"`",
			exitcode.Auth)
		os.Exit(exitcode.Auth)
	case strings.HasPrefix(msg, "HTTP "):
		render.PrintError(out, true, msg, "", exitcode.Error)
		os.Exit(exitcode.Error)
	default:
		render.PrintError(out, true, fmt.Sprintf("network error: %v", err),
			"check the deployment URL and network connectivity",
			exitcode.Network)
		os.Exit(exitcode.Network)
	}
	return nil // unreachable
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
