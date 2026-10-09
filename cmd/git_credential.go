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
	Short: "Git credential helper: broker short-lived VCS tokens for git push",
	Long: `Implements git's credential-helper protocol. Configure git per host:

  git config --global --add credential.https://github.com.helper ""
  git config --global --add credential.https://github.com.helper "!praxis git-credential"
  git config --global credential.https://github.com.useHttpPath true

Add the same two helper lines for https://gitlab.com and
https://bitbucket.org. The empty helper clears the helpers that git inherits
from the system configuration, such as Git Credential Manager (the Git for
Windows default) or osxkeychain. Without it, git also asks them, and they
store the short-lived token.

For a GitHub Enterprise Server on your own domain, scope the helper to that
host too (replace github.acme.com with yours):

  git config --global credential.https://github.acme.com.helper "!praxis git-credential"
  git config --global credential.https://github.acme.com.useHttpPath true

Scope the helper to the specific hosts as shown — the per-host scope is what
tells git to invoke praxis only for those hosts. github.com / GitHub
Enterprise Cloud, gitlab.com and bitbucket.org always mint a brokered token;
a GitHub Enterprise Server host mints the org's stored enterprise credential
when one exists, and emits nothing otherwise so git falls through. Don't
combine with a caching/storing helper (e.g. 'credential.helper store') for
these hosts: it would persist the token to disk, defeating the design.

'useHttpPath' is what makes git send the repository path (e.g.
owner/repo.git); without it git sends only the host, so the mint request
carries no repo and cannot be scoped or audited per-repository.

On 'get', it reads the requested protocol/host/path from stdin and returns a
short-lived, org-brokered token. The username comes from the server per
provider (x-access-token for GitHub, oauth2 for GitLab, x-token-auth for
Bitbucket). GitHub requires the org's GitHub App installation; GitLab and
Bitbucket require a control-plane app account linked in Praxis
(Settings > Integrations > GitLab/Bitbucket > OAuth App Connection).
'store' and 'erase' are no-ops because the token is ephemeral — nothing is
persisted on the laptop.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGitCredential(cmd.OutOrStdout(), cmd.InOrStdin(), args[0], resolveGateway)
	},
}

// resolveGateway returns the active profile's gateway URL + auth headers.
// Honors the global --profile flag, so a repo can pin a helper to one profile
// with `helper = praxis --profile acme git-credential`.
func resolveGateway() (string, map[string]string, error) {
	active, err := credentials.ResolveActive(rootProfile)
	if err != nil {
		return "", nil, err
	}
	if !active.Loaded || active.Profile.Token == "" {
		return "", nil, fmt.Errorf("no credentials for profile %q — run `praxis login`", active.Name)
	}
	return active.Profile.URL, active.Profile.Auth(), nil
}

// isBrokeredHost reports whether host is one we ALWAYS broker — github.com and
// GitHub Enterprise Cloud, plus gitlab.com / bitbucket.org. For these a failed
// mint is surfaced to the user (a real misconfiguration), so git shows why the
// push was refused instead of silently falling back to a password prompt.
//
// A custom host (a GitHub Enterprise Server we can't recognize up front) is
// still attempted — the server vends the org's enterprise credential when it
// has one — but a failure there falls through silently, so an unscoped helper
// never breaks a push to a host we don't broker. The server, not this
// allowlist, is what prevents a token reaching the wrong host: it vends only to
// @facets.cloud callers and only when a stored PAT matches the exact host.
// The GitLab/Bitbucket entries are exact matches (no subdomain logic —
// mirrors the server's _OAUTH_HOSTS allowlist).
func isBrokeredHost(host string) bool {
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
		strings.HasSuffix(h, ".ghe.com") || // GitHub Enterprise Cloud
		h == "gitlab.com" || // VCS OAuth (exact)
		h == "bitbucket.org" // VCS OAuth (exact)
}

// runGitCredential handles one credential-helper invocation.
func runGitCredential(out io.Writer, in io.Reader, op string, gw func() (string, map[string]string, error)) error {
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

	// Only https, and only a real host. Emitting nothing and exiting 0 is git's
	// protocol for "this helper has no credentials" — git falls through.
	if attrs["protocol"] != "https" || attrs["host"] == "" {
		return nil
	}

	err := mintAndEmit(out, attrs, gw)
	if err != nil && isBrokeredHost(attrs["host"]) {
		// A host we always broker failing to mint is a real misconfiguration —
		// surface it so the user sees why the push was refused.
		return err
	}
	// A custom host (a GitHub Enterprise Server we only broker when the org has a
	// matching enterprise PAT) that didn't mint falls through silently, so an
	// unscoped helper never breaks a push to a host we don't broker.
	return nil
}

// mintAndEmit asks the gateway to mint a credential for the requested host and,
// on success, writes git's credential-helper reply to out. It returns an error
// without writing anything when the gateway can't mint one; the caller decides
// whether that error is surfaced or swallowed (see runGitCredential).
func mintAndEmit(out io.Writer, attrs map[string]string, gw func() (string, map[string]string, error)) error {
	body, _ := json.Marshal(map[string]string{
		"host": attrs["host"],
		"path": attrs["path"],
	})

	baseURL, auth, err := gw()
	if err != nil {
		return err
	}
	raw, status, err := callMCP(baseURL, auth, "vcs_cli", "mint_repo_credential", body, 30*time.Second)
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
