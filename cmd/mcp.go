package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/exitcode"
	"github.com/Facets-cloud/praxis-cli/internal/mcpmanifest"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/spf13/cobra"
)

var (
	mcpJSON    bool
	mcpArgs    []string
	mcpBody    string
	mcpTimeout time.Duration
)

func init() {
	mcpCmd.Flags().BoolVar(&mcpJSON, "json", false, "JSON output (default when stdout is non-TTY)")
	mcpCmd.Flags().StringSliceVar(&mcpArgs, "arg", nil, "key=value pair (repeatable); merged into request body")
	mcpCmd.Flags().StringVar(&mcpBody, "body", "", "raw JSON body (use '-' for stdin); overrides --arg")
	mcpCmd.Flags().DurationVar(&mcpTimeout, "timeout", 60*time.Second, "request timeout")
	rootCmd.AddCommand(mcpCmd)
}

var mcpCmd = &cobra.Command{
	Use:   "mcp [<mcp> <fn>]",
	Short: "List or invoke server-side MCP tool functions",
	Long: `Call an MCP tool function exposed by the Praxis server gateway,
or — with no arguments — list every function the gateway exposes.

The CLI never holds AWS / kube / terraform credentials — the server
resolves the org from your API key and runs the call under the
org-managed integration credentials.

Examples:
  praxis mcp                                            # list every mcp + fn
  praxis mcp --json                                     # same, JSON for AI hosts
  praxis mcp cloud_cli list_cloud_integrations
  praxis mcp cloud_cli run_cloud_cli --arg integration_name=aws-prod --arg command='ec2 describe-instances --output json'
  echo '{"integration_name":"aws-prod","command":"ec2 describe-regions"}' | praxis mcp cloud_cli run_cloud_cli --body -`,
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || len(args) == 2 {
			return nil
		}
		return fmt.Errorf("accepts either 0 args (list manifest) or 2 args (<mcp> <fn>); got %d", len(args))
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(mcpJSON, false, out)

		active, err := credentials.ResolveActive("")
		if err != nil {
			return err
		}
		if !active.Loaded || active.Profile.Token == "" {
			render.PrintError(out, asJSON,
				fmt.Sprintf("no credentials for profile %q", active.Name),
				"run `praxis login` (or `praxis login --profile "+active.Name+"`)",
				exitcode.Auth)
			os.Exit(exitcode.Auth)
		}

		// No args → list manifest.
		if len(args) == 0 {
			return runManifestList(out, asJSON, active)
		}

		mcpName := args[0]
		fnName := args[1]

		body, err := buildMCPBody(mcpArgs, mcpBody, cmd.InOrStdin())
		if err != nil {
			render.PrintError(out, asJSON,
				err.Error(),
				"check --arg / --body usage (`praxis mcp --help`)",
				exitcode.Usage)
			os.Exit(exitcode.Usage)
		}

		resp, status, err := callMCP(active.Profile.URL, active.Profile.Token, mcpName, fnName, body, mcpTimeout)
		if err != nil {
			render.PrintError(out, asJSON,
				fmt.Sprintf("network error: %v", err),
				"check the deployment URL and network connectivity",
				exitcode.Network)
			os.Exit(exitcode.Network)
		}

		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			render.PrintError(out, asJSON,
				extractDetail(resp, fmt.Sprintf("HTTP %d from gateway", status)),
				"the API key may be missing or revoked; run `praxis login --profile "+active.Name+"`",
				exitcode.Auth)
			os.Exit(exitcode.Auth)
		case status == http.StatusNotFound:
			render.PrintError(out, asJSON,
				extractDetail(resp, fmt.Sprintf("HTTP %d from gateway", status)),
				fmt.Sprintf("unknown mcp/fn '%s/%s' — check `praxis mcp` documentation for the deployment", mcpName, fnName),
				exitcode.NoConfig)
			os.Exit(exitcode.NoConfig)
		case status >= 500:
			render.PrintError(out, asJSON,
				extractDetail(resp, fmt.Sprintf("HTTP %d from gateway", status)),
				"transient server error — retry, or check the gateway logs",
				exitcode.Network)
			os.Exit(exitcode.Network)
		case status != http.StatusOK:
			render.PrintError(out, asJSON,
				extractDetail(resp, fmt.Sprintf("HTTP %d from gateway", status)),
				"check arguments — gateway rejected the request",
				exitcode.Usage)
			os.Exit(exitcode.Usage)
		}

		// HTTP 200 — print the body verbatim (pass-through MCP envelope).
		// If isError is true on a dict-shape response, exit 1 so callers
		// can detect tool-level failure even with JSON output.
		exitWithToolError := envelopeIsError(resp)

		if asJSON {
			_, _ = out.Write(append(bytes.TrimRight(resp, "\n"), '\n'))
		} else {
			pretty := prettyJSON(resp)
			fmt.Fprintln(out, pretty)
		}

		if exitWithToolError {
			os.Exit(exitcode.Error)
		}
		return nil
	},
}

// buildMCPBody assembles the request body. --body wins; otherwise --arg
// pairs are merged into a flat object. Empty input → empty object so the
// gateway sees `{}` (matching its "body == args" contract).
func buildMCPBody(argFlags []string, bodyFlag string, stdin io.Reader) ([]byte, error) {
	if bodyFlag != "" {
		var raw []byte
		var err error
		if bodyFlag == "-" {
			raw, err = io.ReadAll(stdin)
			if err != nil {
				return nil, fmt.Errorf("read stdin: %w", err)
			}
		} else {
			raw = []byte(bodyFlag)
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			return []byte("{}"), nil
		}
		// Validate it's a JSON object — gateway rejects non-object bodies.
		var probe map[string]any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return nil, fmt.Errorf("--body is not a JSON object: %w", err)
		}
		return raw, nil
	}

	if len(argFlags) == 0 {
		return []byte("{}"), nil
	}

	obj := map[string]any{}
	for _, kv := range argFlags {
		eq := strings.Index(kv, "=")
		if eq <= 0 {
			return nil, fmt.Errorf("invalid --arg %q: expected key=value", kv)
		}
		key := kv[:eq]
		val := kv[eq+1:]
		// Try parsing the value as JSON so callers can pass numbers,
		// booleans, arrays. Falls back to string on parse failure.
		var parsed any
		if json.Unmarshal([]byte(val), &parsed) == nil {
			obj[key] = parsed
		} else {
			obj[key] = val
		}
	}
	return json.Marshal(obj)
}

// runManifestList fetches /v1/mcp/manifest, prints either JSON (AI host
// friendly) or a human-readable grouped listing. Exits the process on
// network/auth errors so the rest of the dispatch RunE doesn't run.
//
// HTTP-level errors come back from mcpmanifest.Fetch as a Go error rather
// than (status, body) since the manifest endpoint has only one success
// shape. We classify the error string to pick an exit code; if the user
// wants the structured detail they can pipe `praxis mcp --json` through
// jq instead.
func runManifestList(out io.Writer, asJSON bool, active credentials.Active) error {
	raw, err := mcpmanifest.Fetch(active.Profile.URL, active.Profile.Token, mcpTimeout)
	if err != nil {
		// Auth-failure shape from Fetch: "manifest fetch returned HTTP 401: ..."
		errStr := err.Error()
		if strings.Contains(errStr, "HTTP 401") || strings.Contains(errStr, "HTTP 403") {
			render.PrintError(out, asJSON,
				errStr,
				"the API key may be missing or revoked; run `praxis login --profile "+active.Name+"`",
				exitcode.Auth)
			os.Exit(exitcode.Auth)
		}
		if strings.Contains(errStr, "HTTP 404") {
			render.PrintError(out, asJSON,
				errStr,
				"the gateway does not expose /v1/mcp/manifest — server may be older than CLI",
				exitcode.NoConfig)
			os.Exit(exitcode.NoConfig)
		}
		render.PrintError(out, asJSON,
			errStr,
			"check the deployment URL and network connectivity",
			exitcode.Network)
		os.Exit(exitcode.Network)
	}

	if asJSON {
		_, _ = out.Write(append(bytes.TrimRight(raw, "\n"), '\n'))
		return nil
	}
	return printManifestPretty(out, raw)
}

// printManifestPretty renders a grouped human listing. Tolerant of
// missing/extra fields — server contract may evolve.
func printManifestPretty(out io.Writer, raw []byte) error {
	var manifest struct {
		Mcps map[string]map[string]struct {
			Description string `json:"description"`
			Args        []struct {
				Name        string `json:"name"`
				Required    bool   `json:"required"`
				Description string `json:"description"`
				Type        string `json:"type"`
			} `json:"args"`
		} `json:"mcps"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		// Server returned something we can't parse — fall back to raw.
		fmt.Fprintln(out, prettyJSON(raw))
		return nil
	}
	if len(manifest.Mcps) == 0 {
		fmt.Fprintln(out, "(no MCPs registered on this gateway)")
		return nil
	}

	mcpNames := make([]string, 0, len(manifest.Mcps))
	for name := range manifest.Mcps {
		mcpNames = append(mcpNames, name)
	}
	sortStrings(mcpNames)

	for i, mcpName := range mcpNames {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s\n", mcpName)
		fns := manifest.Mcps[mcpName]
		fnNames := make([]string, 0, len(fns))
		for name := range fns {
			fnNames = append(fnNames, name)
		}
		sortStrings(fnNames)
		for _, fnName := range fnNames {
			fn := fns[fnName]
			fmt.Fprintf(out, "  %s\n", fnName)
			if fn.Description != "" {
				fmt.Fprintf(out, "    %s\n", fn.Description)
			}
			for _, a := range fn.Args {
				marker := " "
				if a.Required {
					marker = "*"
				}
				fmt.Fprintf(out, "    %s %s — %s\n", marker, a.Name, a.Description)
			}
		}
	}
	fmt.Fprintln(out, "\n(* = required arg)")
	return nil
}

// sortStrings is a tiny dependency-free sort to keep the list deterministic.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// callMCP is the HTTP seam — tests swap it to avoid hitting the network.
var callMCP = func(baseURL, token, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
	if baseURL == "" {
		return nil, 0, errors.New("profile has no URL set")
	}
	client := &http.Client{Timeout: timeout}
	url := strings.TrimRight(baseURL, "/") + "/ai-api/v1/mcp/" + mcp + "/" + fn
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return raw, resp.StatusCode, nil
}

// extractDetail tries to pull `detail` out of a FastAPI-style error body
// for friendlier error printing; falls back to fallback on any failure.
func extractDetail(raw []byte, fallback string) string {
	var probe struct {
		Detail any `json:"detail"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.Detail != nil {
		switch v := probe.Detail.(type) {
		case string:
			return v
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	}
	return fallback
}

// envelopeIsError detects the MCP `{isError: true, ...}` envelope so the
// process can exit non-zero even though the HTTP call succeeded.
func envelopeIsError(raw []byte) bool {
	var probe struct {
		IsError bool `json:"isError"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.IsError
}

func prettyJSON(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(out)
}
