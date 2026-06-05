package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/mcpmanifest"
)

func resetMcpFlags() {
	mcpJSON = false
	mcpArgs = nil
	mcpBody = ""
	mcpTimeout = 60 * time.Second
}

func TestBuildMCPBody_DefaultsToEmptyObject(t *testing.T) {
	body, err := buildMCPBody(nil, "", strings.NewReader(""))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(body) != "{}" {
		t.Errorf("got %s; want {}", body)
	}
}

func TestBuildMCPBody_ArgFlags_Merged(t *testing.T) {
	body, err := buildMCPBody(
		[]string{"name=aws-prod", "count=3", "active=true"},
		"",
		strings.NewReader(""),
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["name"] != "aws-prod" {
		t.Errorf("name = %v", got["name"])
	}
	if got["count"].(float64) != 3 {
		t.Errorf("count = %v (expected 3 as number)", got["count"])
	}
	if got["active"] != true {
		t.Errorf("active = %v (expected bool true)", got["active"])
	}
}

func TestBuildMCPBody_ArgInvalid_ReturnsErr(t *testing.T) {
	_, err := buildMCPBody([]string{"=val"}, "", strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestBuildMCPBody_BodyFlag_Wins(t *testing.T) {
	body, err := buildMCPBody(
		[]string{"name=ignored"},
		`{"only":"this"}`,
		strings.NewReader(""),
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(string(body), `"only"`) {
		t.Errorf("--body should override --arg; got %s", body)
	}
}

func TestBuildMCPBody_BodyStdin(t *testing.T) {
	body, err := buildMCPBody(nil, "-", strings.NewReader(`{"x":1}`))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(string(body), `"x"`) {
		t.Errorf("got %s", body)
	}
}

func TestBuildMCPBody_BodyNotObject_Rejected(t *testing.T) {
	_, err := buildMCPBody(nil, `[1,2,3]`, strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for non-object body")
	}
}

// Regression tests for B7 — `--arg` value parsing.
//
// The bug: pflag's StringSlice does CSV-style parsing on each --arg value,
// which trips on embedded double quotes (`bare " in non-quoted-field`) and
// also splits on commas inside a single value. Real-world callers pass
// raptor commands like `--arg command='create project foo --description
// "Bar"'` — those quotes / commas must reach buildMCPBody intact.
//
// Fix: bind --arg with StringArrayVar (literal per-flag value, no CSV
// splitting). The table cases below ParseFlags on mcpCmd and assert the
// literal value comes through.

func TestMcpArgFlag_LiteralValueParsing(t *testing.T) {
	tests := []struct {
		name     string
		flagArgs []string
		want     []string
	}{
		{
			name:     "embedded quotes preserved",
			flagArgs: []string{"--arg", `command=create project praxis-hello --description "Onboarding sample project"`},
			want:     []string{`command=create project praxis-hello --description "Onboarding sample project"`},
		},
		{
			name:     "embedded commas preserved (no CSV splitting)",
			flagArgs: []string{"--arg", `tags=a,b,c`},
			want:     []string{`tags=a,b,c`},
		},
		{
			name: "repeated flags produce multiple entries",
			flagArgs: []string{
				"--arg", `command=foo "bar"`,
				"--arg", `integration_name=aws-prod`,
			},
			want: []string{`command=foo "bar"`, `integration_name=aws-prod`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetMcpFlags()
			defer resetMcpFlags()

			if err := mcpCmd.ParseFlags(tt.flagArgs); err != nil {
				t.Fatalf("ParseFlags() error = %v", err)
			}
			if len(mcpArgs) != len(tt.want) {
				t.Fatalf("got %d args, want %d: %q", len(mcpArgs), len(tt.want), mcpArgs)
			}
			for i, want := range tt.want {
				if mcpArgs[i] != want {
					t.Errorf("arg[%d] = %q, want %q", i, mcpArgs[i], want)
				}
			}
		})
	}
}

func TestExtractDetail(t *testing.T) {
	got := extractDetail([]byte(`{"detail":"missing key"}`), "fallback")
	if got != "missing key" {
		t.Errorf("got %q", got)
	}
	got = extractDetail([]byte(`not json`), "fallback")
	if got != "fallback" {
		t.Errorf("got %q", got)
	}
}

func TestEnvelopeIsError(t *testing.T) {
	if !envelopeIsError([]byte(`{"isError":true,"content":[]}`)) {
		t.Error("expected true")
	}
	if envelopeIsError([]byte(`{"ok":true}`)) {
		t.Error("expected false")
	}
}

func TestPrettyJSON(t *testing.T) {
	out := prettyJSON([]byte(`{"a":1}`))
	if !strings.Contains(out, "  ") {
		t.Errorf("expected indented; got %q", out)
	}
	// non-JSON falls through unchanged
	if got := prettyJSON([]byte(`abc`)); got != "abc" {
		t.Errorf("got %q", got)
	}
}

// TestCallMCP_PreservesPOSTAcrossRedirect reproduces the issue #18 P0:
// the default host https://askpraxis.ai 301-redirects to www, and Go's
// default http.Client downgrades POST→GET and drops the body on a 301,
// so every invoke hits a body-less GET that 404s and gets misreported
// as "unknown mcp/fn". callMCP must preserve the method, body, and
// Authorization header across the gateway's redirect.
func TestCallMCP_PreservesPOSTAcrossRedirect(t *testing.T) {
	var gotMethod, gotBody, gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/ai-api/v1/mcp/raptor_cli/run_raptor_cli", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ai-api/v1/mcp/raptor_cli/run_raptor_cli/final", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/ai-api/v1/mcp/raptor_cli/run_raptor_cli/final", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := []byte(`{"command":"get projects"}`)
	resp, status, err := callMCP(srv.URL, "sk_test_T", "raptor_cli", "run_raptor_cli", body, 5*time.Second)
	if err != nil {
		t.Fatalf("callMCP error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; resp=%s", status, resp)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method after redirect = %q, want POST (Go must not downgrade POST→GET on 301)", gotMethod)
	}
	if gotBody != string(body) {
		t.Errorf("body after redirect = %q, want %q (body must survive the redirect)", gotBody, body)
	}
	if gotAuth != "Bearer sk_test_T" {
		t.Errorf("Authorization after redirect = %q, want %q", gotAuth, "Bearer sk_test_T")
	}
}

// TestPrettyMCPOutput_UnwrapsSingleTextEnvelope covers issue #18 E3:
// the gateway wraps tool output as
// {"content":[{"type":"text","text":"<escaped JSON>"}]}. For human
// (pretty) output the single text payload should be unwrapped and its
// inner JSON pretty-printed, instead of showing double-encoded escapes.
func TestPrettyMCPOutput_UnwrapsSingleTextEnvelope(t *testing.T) {
	envelope := []byte(`{"content":[{"type":"text","text":"{\"projects\":[\"a\",\"b\"]}"}]}`)
	out := prettyMCPOutput(envelope)
	if strings.Contains(out, `\"`) {
		t.Errorf("output still double-encoded: %q", out)
	}
	if !strings.Contains(out, "projects") || !strings.Contains(out, `"a"`) {
		t.Errorf("inner JSON not surfaced: %q", out)
	}
	if !strings.Contains(out, "\n") {
		t.Errorf("expected pretty/indented inner JSON, got %q", out)
	}
}

// Inner text that isn't JSON should be surfaced verbatim, not wrapped.
func TestPrettyMCPOutput_UnwrapsPlainTextEnvelope(t *testing.T) {
	envelope := []byte(`{"content":[{"type":"text","text":"only for Kubernetes-based environments"}]}`)
	out := prettyMCPOutput(envelope)
	if out != "only for Kubernetes-based environments" {
		t.Errorf("plain-text payload = %q, want unwrapped verbatim", out)
	}
}

// A non-envelope payload falls through to ordinary pretty-printing.
func TestPrettyMCPOutput_NonEnvelopeFallsThrough(t *testing.T) {
	out := prettyMCPOutput([]byte(`{"integrations":["aws-prod"]}`))
	if !strings.Contains(out, "  ") {
		t.Errorf("expected indented JSON, got %q", out)
	}
	if !strings.Contains(out, "integrations") {
		t.Errorf("payload not surfaced: %q", out)
	}
}

// A multi-item content array is not the single-text shape, so it should
// be left as the raw (pretty-printed) envelope rather than guessing.
func TestPrettyMCPOutput_MultiContentNotUnwrapped(t *testing.T) {
	envelope := []byte(`{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`)
	out := prettyMCPOutput(envelope)
	if !strings.Contains(out, "content") {
		t.Errorf("multi-item envelope should fall through to raw pretty, got %q", out)
	}
}

func TestMcpCmd_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetMcpFlags()
	defer resetMcpFlags()

	if err := credentials.Put("default", credentials.Profile{
		URL:      "https://x.test",
		Username: "u@x.com",
		Token:    "sk_test_T",
	}); err != nil {
		t.Fatal(err)
	}

	var capturedURL, capturedToken string
	var capturedBody []byte
	orig := callMCP
	callMCP = func(baseURL, token, mcp, fn string, body []byte, timeout time.Duration) ([]byte, int, error) {
		capturedURL = baseURL + "/ai-api/v1/mcp/" + mcp + "/" + fn
		capturedToken = token
		capturedBody = body
		return []byte(`{"integrations":[{"name":"aws-prod"}]}`), http.StatusOK, nil
	}
	defer func() { callMCP = orig }()

	mcpArgs = []string{"region=us-east-1"}
	var buf bytes.Buffer
	mcpCmd.SetOut(&buf)
	mcpCmd.SetErr(&buf)
	if err := mcpCmd.RunE(mcpCmd, []string{"cloud_cli", "list_cloud_integrations"}); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	if !strings.Contains(capturedURL, "/ai-api/v1/mcp/cloud_cli/list_cloud_integrations") {
		t.Errorf("URL = %q", capturedURL)
	}
	if capturedToken != "sk_test_T" {
		t.Errorf("token = %q", capturedToken)
	}
	if !strings.Contains(string(capturedBody), `"region"`) {
		t.Errorf("body missing region: %s", capturedBody)
	}
	if !strings.Contains(buf.String(), "aws-prod") {
		t.Errorf("output missing tool result: %s", buf.String())
	}
}

// ---------------------------------------------------------------------------
// `praxis mcp` (no args) — list manifest
// ---------------------------------------------------------------------------

func TestPrintManifestPretty_PopulatedManifest(t *testing.T) {
	manifest := []byte(`{"mcps":{"k8s_cli":{"run_k8s_cli":{"description":"run kubectl","args":[{"name":"command","required":true,"description":"the kubectl command","type":"string"},{"name":"namespace","required":false,"description":"override namespace","type":"string"}]}}}}`)
	var buf bytes.Buffer
	if err := printManifestPretty(&buf, manifest); err != nil {
		t.Fatalf("err = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"k8s_cli", "run_k8s_cli", "run kubectl", "command", "namespace", "* = required arg"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n--- got ---\n%s", want, out)
		}
	}
	// Required marker should appear next to `command`, not `namespace`.
	idxCmd := strings.Index(out, "command —")
	idxNs := strings.Index(out, "namespace —")
	if idxCmd < 0 || idxNs < 0 {
		t.Fatalf("missing arg lines in output:\n%s", out)
	}
	// Check the rune before "command —": should be '*'.
	if !strings.Contains(out[max0(idxCmd-3):idxCmd], "*") {
		t.Errorf("command should be marked required (* prefix); got %s", out[max0(idxCmd-3):idxCmd])
	}
	if strings.Contains(out[max0(idxNs-3):idxNs], "*") {
		t.Errorf("namespace should NOT be marked required")
	}
}

func max0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

func TestMcpCmd_NoArgs_JsonPassthrough(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetMcpFlags()
	defer resetMcpFlags()

	if err := credentials.Put("default", credentials.Profile{
		URL: "https://x.test", Username: "u@x.com", Token: "sk_test_T",
	}); err != nil {
		t.Fatal(err)
	}

	manifest := []byte(`{"mcps":{"cloud_cli":{}}}`)
	orig := mcpmanifest.Fetch
	mcpmanifest.Fetch = func(_, _ string, _ time.Duration) ([]byte, error) {
		return manifest, nil
	}
	defer func() { mcpmanifest.Fetch = orig }()

	mcpJSON = true
	var buf bytes.Buffer
	mcpCmd.SetOut(&buf)
	mcpCmd.SetErr(&buf)
	if err := mcpCmd.RunE(mcpCmd, []string{}); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != strings.TrimSpace(string(manifest)) {
		t.Errorf("--json output mismatch:\n got: %q\nwant: %q", got, manifest)
	}
}

// ---------------------------------------------------------------------------
// Args validator: 1 positional arg is invalid (must be 0 or 2)
// ---------------------------------------------------------------------------

func TestMcpCmd_OneArg_Rejected(t *testing.T) {
	if err := mcpCmd.Args(mcpCmd, []string{"k8s_cli"}); err == nil {
		t.Fatal("expected error for 1 arg, got nil")
	}
	if err := mcpCmd.Args(mcpCmd, []string{}); err != nil {
		t.Errorf("0 args should be valid: %v", err)
	}
	if err := mcpCmd.Args(mcpCmd, []string{"a", "b"}); err != nil {
		t.Errorf("2 args should be valid: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Pretty-printer tolerates malformed/empty manifest gracefully
// ---------------------------------------------------------------------------

func TestPrintManifestPretty_EmptyMcps(t *testing.T) {
	var buf bytes.Buffer
	if err := printManifestPretty(&buf, []byte(`{"mcps":{}}`)); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(buf.String(), "no MCPs registered") {
		t.Errorf("output missing empty-state message: %s", buf.String())
	}
}

func TestPrintManifestPretty_MalformedFallsBackToRaw(t *testing.T) {
	var buf bytes.Buffer
	if err := printManifestPretty(&buf, []byte("not json")); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(buf.String(), "not json") {
		t.Errorf("output should contain raw fallback: %s", buf.String())
	}
}

// Sanity check on the testing seam — referenced once so the import isn't
// flagged as unused if all the tests above are temporarily disabled.
var _ = errors.New
