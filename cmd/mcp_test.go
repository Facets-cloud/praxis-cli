package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

func resetMcpFlags() {
	mcpProfile = ""
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
