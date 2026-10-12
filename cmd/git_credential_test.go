package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// bearer builds the Auth() header map a Bearer-mode profile produces.
// Shared across cmd tests that thread an auth header map through a seam.
func bearer(tok string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + tok}
}

func TestGitCredentialGet_EmitsUsernamePassword(t *testing.T) {
	orig := callMCP
	defer func() { callMCP = orig }()
	// mint_repo_credential returns an MCP envelope whose text is JSON.
	callMCP = func(baseURL string, auth map[string]string, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		if mcp != "vcs_cli" || fn != "mint_repo_credential" {
			t.Fatalf("unexpected call %s/%s", mcp, fn)
		}
		env := `{"content":[{"type":"text","text":"{\"username\":\"x-access-token\",\"password\":\"ghs_abc\",\"expires_at\":null}"}]}`
		return []byte(env), 200, nil
	}

	in := strings.NewReader("protocol=https\nhost=github.com\npath=owner/x\n\n")
	var out bytes.Buffer
	err := runGitCredential(&out, in, "get",
		func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "username=x-access-token") || !strings.Contains(got, "password=ghs_abc") {
		t.Fatalf("bad output: %q", got)
	}
}

func TestGitCredentialStoreErase_NoOp(t *testing.T) {
	for _, op := range []string{"store", "erase"} {
		var out bytes.Buffer
		in := strings.NewReader("protocol=https\nhost=github.com\n\n")
		if err := runGitCredential(&out, in, op,
			func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil }); err != nil {
			t.Fatalf("%s should be no-op, got %v", op, err)
		}
		if out.Len() != 0 {
			t.Fatalf("%s should emit nothing, got %q", op, out.String())
		}
	}
}

func TestGitCredentialGet_ParsesHostAndPath(t *testing.T) {
	orig := callMCP
	defer func() { callMCP = orig }()
	var sentBody []byte
	callMCP = func(baseURL string, auth map[string]string, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		sentBody = body
		env := `{"content":[{"type":"text","text":"{\"username\":\"x-access-token\",\"password\":\"ghs_abc\"}"}]}`
		return []byte(env), 200, nil
	}
	in := strings.NewReader("protocol=https\nhost=github.com\npath=owner/x\n\n")
	var out bytes.Buffer
	if err := runGitCredential(&out, in, "get",
		func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil }); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(sentBody), `"host":"github.com"`) ||
		!strings.Contains(string(sentBody), `"path":"owner/x"`) {
		t.Fatalf("body missing host/path: %s", sentBody)
	}
}

// assertSilentFallThrough runs `get` with an input that must short-circuit
// BEFORE any gateway call — a non-https protocol or an empty host — and requires
// the helper to emit nothing, error nothing, and never reach the gateway (git's
// protocol for "this helper has no credentials").
func assertSilentFallThrough(t *testing.T, protocol, host string) {
	t.Helper()
	orig := callMCP
	defer func() { callMCP = orig }()
	called := false
	callMCP = func(baseURL string, auth map[string]string, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		called = true
		return nil, 200, nil
	}

	var out bytes.Buffer
	in := strings.NewReader("protocol=" + protocol + "\nhost=" + host + "\n\n")
	if err := runGitCredential(&out, in, "get",
		func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil }); err != nil {
		t.Fatalf("expected silent fall-through, got err %v", err)
	}
	if called {
		t.Fatal("helper must not call the gateway")
	}
	if out.Len() != 0 {
		t.Fatalf("must emit no credential, got %q", out.String())
	}
}

func TestGitCredentialGet_EmptyHostShortCircuits(t *testing.T) {
	// A missing host never reaches the gateway.
	assertSilentFallThrough(t, "https", "")
}

// assertRefusalFallsThrough mints against a gateway that vends nothing (the
// server's answer for a host with no matching enterprise PAT, or a non-facets
// caller) and requires the helper to emit nothing and NOT error — git then
// falls through. The gateway IS reached: we can't know a custom GitHub
// Enterprise Server host up front, so the server, not a client allowlist, is
// what refuses to hand a token to the wrong host.
func assertRefusalFallsThrough(t *testing.T, host string) {
	t.Helper()
	orig := callMCP
	defer func() { callMCP = orig }()
	callMCP = func(baseURL string, auth map[string]string, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		return []byte(`{"isError":true,"content":[{"type":"text","text":"no credential for host"}]}`), 200, nil
	}
	var out bytes.Buffer
	in := strings.NewReader("protocol=https\nhost=" + host + "\n\n")
	if err := runGitCredential(&out, in, "get",
		func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil }); err != nil {
		t.Fatalf("a non-brokered host must fall through silently, got err %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("must emit no credential, got %q", out.String())
	}
}

func TestGitCredentialGet_NonBrokeredHostFallsThrough(t *testing.T) {
	// Impostor/suffix traps (gitlab/bitbucket are exact-match brokered hosts).
	// Each now reaches the gateway, is refused, and falls through — no token.
	for _, host := range []string{
		"evil.example.com",
		"github.com.evil.test",
		"gitlab.com.evil.test",
		"bitbucket.org.evil.test",
		"sub.gitlab.com",
	} {
		t.Run(host, func(t *testing.T) { assertRefusalFallsThrough(t, host) })
	}
}

func TestGitCredentialGet_EnterpriseServerHostMints(t *testing.T) {
	// A custom GitHub Enterprise Server host the server CAN vend for: the
	// helper forwards the host and emits the returned enterprise credential.
	orig := callMCP
	defer func() { callMCP = orig }()
	var sentBody []byte
	callMCP = func(baseURL string, auth map[string]string, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		sentBody = body
		env := `{"content":[{"type":"text","text":"{\"username\":\"x-access-token\",\"password\":\"ghp_ent\"}"}]}`
		return []byte(env), 200, nil
	}
	var out bytes.Buffer
	in := strings.NewReader("protocol=https\nhost=github.acme.com\npath=team/repo\n\n")
	if err := runGitCredential(&out, in, "get",
		func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil }); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out.String(), "host=github.acme.com\n") ||
		!strings.Contains(out.String(), "password=ghp_ent\n") {
		t.Fatalf("expected enterprise credential for the custom host, got %q", out.String())
	}
	if !strings.Contains(string(sentBody), `"host":"github.acme.com"`) {
		t.Fatalf("custom host must be forwarded to the gateway, got %s", sentBody)
	}
}

func TestGitCredentialGet_BrokeredHostSurfacesError(t *testing.T) {
	// A host we ALWAYS broker that fails to mint must surface the error, not
	// fall through — otherwise git silently prompts for a password and hides why.
	orig := callMCP
	defer func() { callMCP = orig }()
	callMCP = func(baseURL string, auth map[string]string, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		return []byte(`{"isError":true,"content":[{"type":"text","text":"boom"}]}`), 200, nil
	}
	var out bytes.Buffer
	in := strings.NewReader("protocol=https\nhost=github.com\n\n")
	err := runGitCredential(&out, in, "get",
		func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil })
	if err == nil {
		t.Fatal("a brokered host that fails to mint must return an error, not fall through")
	}
	if out.Len() != 0 {
		t.Fatalf("no partial output on error, got %q", out.String())
	}
}

func TestGitCredentialGet_RefusesNonHTTPSProtocol(t *testing.T) {
	for _, proto := range []string{"http", "ssh", ""} {
		t.Run(proto, func(t *testing.T) { assertSilentFallThrough(t, proto, "github.com") })
	}
}

func TestGitCredentialGet_AllowsBrokeredHosts(t *testing.T) {
	// Per-provider usernames come from the SERVER's response and must reach
	// git unchanged (x-access-token is only a fallback for empty username).
	tests := []struct {
		host     string
		username string
		token    string
	}{
		{"github.com", "x-access-token", "ghs_abc"},
		{"GitHub.COM", "x-access-token", "ghs_abc"},
		{"acme.ghe.com", "x-access-token", "ghs_abc"},
		{"github.com:443", "x-access-token", "ghs_abc"},
		{"gitlab.com", "oauth2", "glat_abc"},
		{"bitbucket.org", "x-token-auth", "bbat_abc"},
	}
	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			orig := callMCP
			defer func() { callMCP = orig }()
			callMCP = func(baseURL string, auth map[string]string, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
				env := `{"content":[{"type":"text","text":"{\"username\":\"` + tc.username +
					`\",\"password\":\"` + tc.token + `\"}"}]}`
				return []byte(env), 200, nil
			}
			var out bytes.Buffer
			in := strings.NewReader("protocol=https\nhost=" + tc.host + "\n\n")
			if err := runGitCredential(&out, in, "get",
				func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil }); err != nil {
				t.Fatalf("err: %v", err)
			}
			if !strings.Contains(out.String(), "username="+tc.username+"\n") {
				t.Fatalf("expected username %q to pass through, got %q", tc.username, out.String())
			}
			if !strings.Contains(out.String(), "password="+tc.token+"\n") {
				t.Fatalf("expected a credential, got %q", out.String())
			}
		})
	}
}

func TestGitCredential_RejectsUnknownOperation(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("protocol=https\nhost=github.com\n\n")
	err := runGitCredential(&out, in, "gte",
		func() (string, map[string]string, error) { return "https://gw", bearer("tok"), nil })
	if err == nil {
		t.Fatal("unknown operation must return an error, not silently succeed")
	}
	// Assert on content so an unrelated error can't make this pass.
	if !strings.Contains(err.Error(), "unsupported credential operation") ||
		!strings.Contains(err.Error(), `"gte"`) {
		t.Fatalf("error should name the unsupported op, got: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unknown operation must emit nothing, got %q", out.String())
	}
}
