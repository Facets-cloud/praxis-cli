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

On 'get', it reads the requested host/path from stdin and returns a
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

// runGitCredential handles one credential-helper invocation.
func runGitCredential(out io.Writer, in io.Reader, op string, gw func() (string, string, error)) error {
	if op != "get" {
		// store / erase: ephemeral tokens, nothing to persist or revoke.
		return nil
	}

	attrs := parseCredentialInput(in)
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
