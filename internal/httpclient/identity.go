package httpclient

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"runtime"
	"strings"
)

// Version and Command name this build and invocation on every request. The
// root command sets them before it runs; Command is the path without
// "praxis", e.g. "mcp".
var (
	Version = "dev"
	Command string
)

// session is one id per process unless an agent host or CI run names one.
var session = sessionID(os.Getenv)

// Session is the session id this process sends in X-Facets-Client. The
// friction report history uses it to group the commands of one session.
func Session() string { return session }

// CIName is the CI system that runs this process ("other" for an unknown one
// that sets CI), and empty outside CI.
func CIName() string { return ciName(os.Getenv) }

// CIEnvVars lists every environment variable that marks a CI run, so a test
// can clear them all.
func CIEnvVars() []string {
	out := make([]string, 0, len(ciMarkers)+1)
	for _, m := range ciMarkers {
		out = append(out, m.env)
	}
	return append(out, "CI")
}

// identityTransport adds the identity headers, so no call site can forget them.
type identityTransport struct{}

func (identityTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	setIdentity(r.Header)
	return http.DefaultTransport.RoundTrip(r)
}

// CloseIdleConnections keeps http.Client.CloseIdleConnections working.
func (identityTransport) CloseIdleConnections() {
	if t, ok := http.DefaultTransport.(interface{ CloseIdleConnections() }); ok {
		t.CloseIdleConnections()
	}
}

// setIdentity names this build on a Praxis request:
//
//	User-Agent:       praxis/1.14.0 (darwin/arm64)
//	X-Facets-Client:  praxis/1.14.0 (darwin/arm64; host=claude-code; session=claude-…; ci=github-actions; go=go1.25.1)
//	X-Facets-Command: mcp
//
// X-Facets-Client is a product token and a "; "-separated list. The first item
// is GOOS/GOARCH; every other item is key=value, and a reader ignores keys it
// does not know. raptor sends the same grammar.
func setIdentity(h http.Header) {
	v := versionToken(Version)
	platform := runtime.GOOS + "/" + runtime.GOARCH
	h.Set("User-Agent", "praxis/"+v+" ("+platform+")")
	items := []string{platform}
	for _, kv := range [][2]string{{"host", agentHost(os.Getenv)}, {"session", session}, {"ci", ciName(os.Getenv)}, {"go", runtime.Version()}} {
		if t := token(kv[1]); t != "" {
			items = append(items, kv[0]+"="+t)
		}
	}
	h.Set("X-Facets-Client", "praxis/"+v+" ("+strings.Join(items, "; ")+")")
	if Command != "" {
		h.Set("X-Facets-Command", Command)
	}
}

var hostMarkers = []struct{ env, host string }{
	{"CLAUDECODE", "claude-code"},
	{"CODEX_SANDBOX", "codex"},
	{"CODEX_SANDBOX_NETWORK_DISABLED", "codex"},
	{"GEMINI_CLI", "gemini-cli"},
	{"CURSOR_AGENT", "cursor"},
}

// agentHost is the AI host that runs this process; empty means a person.
func agentHost(getenv func(string) string) string {
	for _, m := range hostMarkers {
		if getenv(m.env) != "" {
			return m.host
		}
	}
	return ""
}

var ciMarkers = []struct{ env, name string }{
	{"GITHUB_ACTIONS", "github-actions"},
	{"GITLAB_CI", "gitlab"},
	{"BUILDKITE", "buildkite"},
	{"JENKINS_URL", "jenkins"},
	{"CIRCLECI", "circleci"},
	{"TF_BUILD", "azure-pipelines"},
	{"BITBUCKET_BUILD_NUMBER", "bitbucket"},
	{"CODEBUILD_BUILD_ID", "codebuild"},
}

// ciName is the CI system that runs this process, "other" for an unknown one
// that sets CI, and empty outside CI.
func ciName(getenv func(string) string) string {
	for _, m := range ciMarkers {
		if getenv(m.env) != "" {
			return m.name
		}
	}
	if getenv("CI") != "" {
		return "other"
	}
	return ""
}

var sessionSources = []struct{ env, prefix string }{
	{"CLAUDE_CODE_SESSION_ID", "claude-"},
	{"GITHUB_RUN_ID", "gh-"},
	{"CI_PIPELINE_ID", "gitlab-"},
	{"BUILDKITE_BUILD_ID", "buildkite-"},
}

// sessionID joins the calls of one agent session or CI run; otherwise it is a
// fresh id for this process.
func sessionID(getenv func(string) string) string {
	for _, s := range sessionSources {
		if v := getenv(s.env); v != "" {
			if len(v) > 64 {
				v = v[:64]
			}
			return s.prefix + v
		}
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "run-unknown"
	}
	return "run-" + hex.EncodeToString(b[:])
}

// versionToken is the version as an HTTP product-version token: no "/" or ":",
// and "dev" when nothing is left.
func versionToken(s string) string {
	if v := strings.NewReplacer("/", "", ":", "").Replace(token(s)); v != "" {
		return v
	}
	return "dev"
}

// token keeps only characters that cannot break the header grammar.
func token(s string) string {
	var b strings.Builder
	for _, c := range strings.TrimSpace(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '-', c == '_', c == '.', c == '+', c == '/', c == ':':
			b.WriteRune(c)
		}
	}
	return b.String()
}
