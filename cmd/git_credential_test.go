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
	_ = runGitCredential(&out, in, "get",
		func() (string, string, error) { return "https://gw", "tok", nil })
	if !strings.Contains(string(sentBody), `"host":"github.com"`) ||
		!strings.Contains(string(sentBody), `"path":"owner/x"`) {
		t.Fatalf("body missing host/path: %s", sentBody)
	}
}
