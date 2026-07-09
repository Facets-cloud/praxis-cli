package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestGitCredentialGet_EmitsUsernamePassword(t *testing.T) {
	orig := callMCP
	defer func() { callMCP = orig }()
	// mint_repo_credential returns an MCP envelope whose text is JSON.
	callMCP = func(baseURL, token, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		if mcp != "vcs_cli" || fn != "mint_repo_credential" {
			t.Fatalf("unexpected call %s/%s", mcp, fn)
		}
		env := `{"content":[{"type":"text","text":"{\"username\":\"x-access-token\",\"password\":\"ghs_abc\",\"expires_at\":null}"}]}`
		return []byte(env), 200, nil
	}

	in := strings.NewReader("protocol=https\nhost=github.com\npath=owner/x\n\n")
	var out bytes.Buffer
	err := runGitCredential(&out, in, "get",
		func() (string, string, error) { return "https://gw", "tok", nil })
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
			func() (string, string, error) { return "https://gw", "tok", nil }); err != nil {
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
	callMCP = func(baseURL, token, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		sentBody = body
		env := `{"content":[{"type":"text","text":"{\"username\":\"x-access-token\",\"password\":\"ghs_abc\"}"}]}`
		return []byte(env), 200, nil
	}
	in := strings.NewReader("protocol=https\nhost=github.com\npath=owner/x\n\n")
	var out bytes.Buffer
	if err := runGitCredential(&out, in, "get",
		func() (string, string, error) { return "https://gw", "tok", nil }); err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(sentBody), `"host":"github.com"`) ||
		!strings.Contains(string(sentBody), `"path":"owner/x"`) {
		t.Fatalf("body missing host/path: %s", sentBody)
	}
}

// A helper configured unscoped (`credential.helper` rather than
// `credential.https://github.com.helper`) is invoked by git for EVERY host.
// Verified with a real `git ls-remote https://gitlab.com/...`, which feeds the
// helper `host=gitlab.com`. Minting there would hand a GitHub token to a third
// party, so non-GitHub hosts must never reach the gateway.
// assertSilentFallThrough runs `get` with the given credential input and
// requires the helper to emit nothing, error nothing, and never reach the
// gateway — git's protocol for "this helper has no credentials".
func assertSilentFallThrough(t *testing.T, protocol, host string) {
	t.Helper()
	orig := callMCP
	defer func() { callMCP = orig }()
	called := false
	callMCP = func(baseURL, token, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		called = true
		return nil, 200, nil
	}

	var out bytes.Buffer
	in := strings.NewReader("protocol=" + protocol + "\nhost=" + host + "\n\n")
	if err := runGitCredential(&out, in, "get",
		func() (string, string, error) { return "https://gw", "tok", nil }); err != nil {
		t.Fatalf("expected silent fall-through, got err %v", err)
	}
	if called {
		t.Fatal("helper must not call the gateway")
	}
	if out.Len() != 0 {
		t.Fatalf("must emit no credential, got %q", out.String())
	}
}

func TestGitCredentialGet_RefusesNonGitHubHosts(t *testing.T) {
	// "github.com.evil.test" is the suffix trap; "" is a missing host.
	for _, host := range []string{"gitlab.com", "evil.example.com", "github.com.evil.test", ""} {
		t.Run(host, func(t *testing.T) { assertSilentFallThrough(t, "https", host) })
	}
}

func TestGitCredentialGet_RefusesNonHTTPSProtocol(t *testing.T) {
	for _, proto := range []string{"http", "ssh", ""} {
		t.Run(proto, func(t *testing.T) { assertSilentFallThrough(t, proto, "github.com") })
	}
}

func TestGitCredentialGet_AllowsGitHubHosts(t *testing.T) {
	for _, host := range []string{"github.com", "GitHub.COM", "acme.ghe.com", "github.com:443"} {
		t.Run(host, func(t *testing.T) {
			orig := callMCP
			defer func() { callMCP = orig }()
			callMCP = func(baseURL, token, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
				env := `{"content":[{"type":"text","text":"{\"username\":\"x-access-token\",\"password\":\"ghs_abc\"}"}]}`
				return []byte(env), 200, nil
			}
			var out bytes.Buffer
			in := strings.NewReader("protocol=https\nhost=" + host + "\n\n")
			if err := runGitCredential(&out, in, "get",
				func() (string, string, error) { return "https://gw", "tok", nil }); err != nil {
				t.Fatalf("err: %v", err)
			}
			if !strings.Contains(out.String(), "password=ghs_abc") {
				t.Fatalf("expected a credential, got %q", out.String())
			}
		})
	}
}

func TestGitCredential_RejectsUnknownOperation(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("protocol=https\nhost=github.com\n\n")
	err := runGitCredential(&out, in, "gte",
		func() (string, string, error) { return "https://gw", "tok", nil })
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
