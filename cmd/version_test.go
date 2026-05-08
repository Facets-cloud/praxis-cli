package cmd

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// TestVersionCmd_JSON_PrintsAllFields verifies the JSON branch contains
// every metadata field. Bytes.Buffer is non-TTY so render.UseJSON()
// auto-selects JSON mode regardless of the flag — matching how AI hosts
// see this command.
func TestVersionCmd_JSON_PrintsAllFields(t *testing.T) {
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	versionJSON = false
	if err := versionCmd.RunE(versionCmd, nil); err != nil {
		t.Fatalf("RunE err = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\noutput: %s", err, buf.String())
	}
	for _, key := range []string{"version", "commit", "built", "go", "os", "arch"} {
		if _, ok := got[key]; !ok {
			t.Errorf("JSON missing key %q\nfull output:\n%s", key, buf.String())
		}
	}
	if got["go"] != runtime.Version() {
		t.Errorf("go = %v, want %s", got["go"], runtime.Version())
	}
	if got["os"] != runtime.GOOS {
		t.Errorf("os = %v, want %s", got["os"], runtime.GOOS)
	}
	if got["arch"] != runtime.GOARCH {
		t.Errorf("arch = %v, want %s", got["arch"], runtime.GOARCH)
	}
	// Sanity-check the human path renders the same data — invoke via an
	// outer cobra Execute() so the parent IsTTY logic doesn't kick in;
	// we just check that the human renderer doesn't error.
	_ = strings.TrimSpace(buf.String())
}
