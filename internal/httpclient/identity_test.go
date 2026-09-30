package httpclient

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestAgentHostAndCIName(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		host, ci string
	}{
		{"person", nil, "", ""},
		{"claude code", map[string]string{"CLAUDECODE": "1"}, "claude-code", ""},
		{"codex", map[string]string{"CODEX_SANDBOX": "seatbelt"}, "codex", ""},
		{"gemini", map[string]string{"GEMINI_CLI": "1"}, "gemini-cli", ""},
		{"cursor", map[string]string{"CURSOR_AGENT": "1"}, "cursor", ""},
		{"github actions", map[string]string{"GITHUB_ACTIONS": "true", "CI": "true"}, "", "github-actions"},
		{"gitlab", map[string]string{"GITLAB_CI": "true"}, "", "gitlab"},
		{"unknown ci", map[string]string{"CI": "1"}, "", "other"},
		{"agent inside ci", map[string]string{"CLAUDECODE": "1", "BUILDKITE": "true"}, "claude-code", "buildkite"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentHost(envOf(tc.env)); got != tc.host {
				t.Errorf("agentHost = %q, want %q", got, tc.host)
			}
			if got := ciName(envOf(tc.env)); got != tc.ci {
				t.Errorf("ciName = %q, want %q", got, tc.ci)
			}
		})
	}
}

func TestSessionID(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"claude session wins", map[string]string{"CLAUDE_CODE_SESSION_ID": "abc", "GITHUB_RUN_ID": "9"}, "claude-abc"},
		{"github run", map[string]string{"GITHUB_RUN_ID": "9"}, "gh-9"},
		{"long id is cut", map[string]string{"CI_PIPELINE_ID": strings.Repeat("x", 100)}, "gitlab-" + strings.Repeat("x", 64)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionID(envOf(tc.env)); got != tc.want {
				t.Errorf("sessionID = %q, want %q", got, tc.want)
			}
		})
	}
	a, b := sessionID(envOf(nil)), sessionID(envOf(nil))
	if !regexp.MustCompile(`^run-[0-9a-f]{16}$`).MatchString(a) || a == b {
		t.Errorf("per-process ids = %q, %q; want two distinct run-<16 hex>", a, b)
	}
}

func TestToken(t *testing.T) {
	tests := []struct{ in, want string }{
		{" 1.14.0-rc1+abc ", "1.14.0-rc1+abc"},
		{"evil; x=(y)", "evilxy"},
		{"a b\n", "ab"},
	}
	for _, tc := range tests {
		if got := token(tc.in); got != tc.want {
			t.Errorf("token(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Every request from New carries the identity, including one that follows a
// redirect, and the caller's own headers survive.
func TestNew_SendsIdentity(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	oldV, oldC := Version, Command
	Version, Command = "1.14.0", "mcp"
	t.Cleanup(func() { Version, Command = oldV, oldC })

	var got []http.Header
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Clone())
	}))
	defer target.Close()
	from := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Clone())
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer from.Close()

	req, _ := http.NewRequest(http.MethodGet, from.URL, nil)
	req.Header.Set("X-Facets-Username", "u@x")
	resp, err := New(5 * time.Second).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if req.Header.Get("X-Facets-Client") != "" {
		t.Error("the caller's request was modified")
	}

	platform := runtime.GOOS + "/" + runtime.GOARCH
	grammar := regexp.MustCompile(`^praxis/1\.14\.0 \(` + regexp.QuoteMeta(platform) + `(; [a-z]+=[^ ;()]+)+\)$`)
	if len(got) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(got))
	}
	for i, h := range got {
		if ua := h.Get("User-Agent"); ua != "praxis/1.14.0 ("+platform+")" {
			t.Errorf("request %d User-Agent = %q", i, ua)
		}
		c := h.Get("X-Facets-Client")
		if !grammar.MatchString(c) || !strings.Contains(c, "; host=claude-code; session=") || strings.Contains(c, "ci=") {
			t.Errorf("request %d X-Facets-Client = %q", i, c)
		}
		if cmd := h.Get("X-Facets-Command"); cmd != "mcp" {
			t.Errorf("request %d X-Facets-Command = %q", i, cmd)
		}
		if h.Get("X-Facets-Username") != "u@x" {
			t.Errorf("request %d lost the caller's header", i)
		}
	}
}
