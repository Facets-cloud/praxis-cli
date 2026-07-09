package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(gitCredentialCmd)
}

var gitCredentialCmd = &cobra.Command{
	Use:   "git-credential <get|store|erase>",
	Short: "Git credential helper: broker a short-lived GitHub token for git push",
	Long: `Implements git's credential-helper protocol. Configure git with:

  git config --global credential.https://github.com.helper "!praxis git-credential"
  git config --global credential.https://github.com.useHttpPath true

Scope the helper to https://github.com as shown. An unscoped
'credential.helper' is invoked by git for every host, and this helper only
ever mints for GitHub — it emits nothing for other hosts so git falls through.

'useHttpPath' is what makes git send the repository path (e.g.
owner/repo.git); without it git sends only the host, so the mint request
carries no repo and cannot be scoped or audited per-repository.

On 'get', it reads the requested protocol/host/path from stdin and returns a
short-lived, org-brokered token (username x-access-token). 'store' and 'erase'
are no-ops because the token is ephemeral — nothing is persisted on the laptop.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGitCredential(cmd.OutOrStdout(), cmd.InOrStdin(), args[0], resolveGateway)
	},
}

// resolveGateway returns the active profile's gateway URL + token.
func resolveGateway() (string, string, error) {
	active, err := credentials.ResolveActive("")
	if err != nil {
		return "", "", err
	}
	if !active.Loaded || active.Profile.Token == "" {
		return "", "", fmt.Errorf("no credentials for profile %q — run `praxis login`", active.Name)
	}
	return active.Profile.URL, active.Profile.Token, nil
}

// isGitHubHost reports whether a brokered GitHub token may be handed to host.
//
// git invokes a credential helper for whatever host it is talking to. If the
// helper is configured unscoped (`credential.helper` rather than
// `credential.https://github.com.helper`), a push to any other host would
// otherwise receive our GitHub token. Fail closed on anything unrecognized.
func isGitHubHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	// Strip any :port git may append.
	if i := strings.IndexByte(h, ':'); i > 0 {
		h = h[:i]
	}
	return h == "github.com" ||
		strings.HasSuffix(h, ".github.com") || // github.com subdomains
		strings.HasSuffix(h, ".ghe.com") // GitHub Enterprise Cloud
}

// runGitCredential handles one credential-helper invocation.
func runGitCredential(out io.Writer, in io.Reader, op string, gw func() (string, string, error)) error {
	switch op {
	case "get":
		// handled below
	case "store", "erase":
		// Ephemeral tokens: nothing to persist or revoke.
		return nil
	default:
		return fmt.Errorf("unsupported credential operation %q (want get, store, or erase)", op)
	}

	attrs := parseCredentialInput(in)

	// Never mint for a host that isn't GitHub, or over plaintext. Emitting
	// nothing and exiting 0 is git's protocol for "this helper has no
	// credentials" — git then falls through to the next helper.
	if attrs["protocol"] != "https" || !isGitHubHost(attrs["host"]) {
		return nil
	}

	body, _ := json.Marshal(map[string]string{
		"host": attrs["host"],
		"path": attrs["path"],
	})

	baseURL, token, err := gw()
	if err != nil {
		return err
	}
	raw, status, err := callMCP(baseURL, token, "vcs_cli", "mint_repo_credential", body, 30*time.Second)
	if err != nil {
		return fmt.Errorf("gateway call failed: %w", err)
	}
	if status != 200 {
		return fmt.Errorf("gateway returned HTTP %d: %s", status, extractDetail(raw, "mint failed"))
	}

	username, password, err := parseGhEnvelope(raw)
	if err != nil {
		return err
	}
	if attrs["protocol"] != "" {
		fmt.Fprintf(out, "protocol=%s\n", attrs["protocol"])
	}
	if attrs["host"] != "" {
		fmt.Fprintf(out, "host=%s\n", attrs["host"])
	}
	fmt.Fprintf(out, "username=%s\n", username)
	fmt.Fprintf(out, "password=%s\n", password)
	return nil
}

// parseCredentialInput reads the key=value lines git feeds on stdin,
// stopping at a blank line or EOF.
func parseCredentialInput(in io.Reader) map[string]string {
	attrs := map[string]string{}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if eq := strings.IndexByte(line, '='); eq > 0 {
			attrs[line[:eq]] = line[eq+1:]
		}
	}
	return attrs
}

// parseGhEnvelope unwraps the MCP envelope ({content:[{text}]}) whose text is
// the JSON {username,password,...} from mint_repo_credential.
var parseGhEnvelope = func(raw []byte) (string, string, error) {
	var env struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", "", fmt.Errorf("parse envelope: %w", err)
	}
	if env.IsError || len(env.Content) == 0 {
		return "", "", fmt.Errorf("gateway error: %s", string(raw))
	}
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(env.Content[0].Text), &creds); err != nil {
		return "", "", fmt.Errorf("parse credential: %w", err)
	}
	if creds.Password == "" {
		return "", "", fmt.Errorf("gateway returned empty token")
	}
	if creds.Username == "" {
		creds.Username = "x-access-token"
	}
	return creds.Username, creds.Password, nil
}
