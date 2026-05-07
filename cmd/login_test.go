package cmd

import (
	"strings"
	"testing"
)

func TestBuildLoginURL_Valid(t *testing.T) {
	got, err := buildLoginURL("https://app.example.com", 51234, "nonce-abc")
	if err != nil {
		t.Fatalf("buildLoginURL err = %v", err)
	}
	for _, want := range []string{
		"https://app.example.com/ui/ai/settings/api-keys",
		"cli_callback=http%3A%2F%2F127.0.0.1%3A51234%2Fkey",
		"cli_session=nonce-abc",
		"suggested_name=praxis-cli",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("buildLoginURL output missing %q\nfull: %s", want, got)
		}
	}
}

func TestBuildLoginURL_Malformed(t *testing.T) {
	// url.Parse rejects control characters in the scheme/host portion.
	_, err := buildLoginURL("http://[::1", 8080, "nonce")
	if err == nil {
		t.Fatal("expected error for malformed URL, got nil")
	}
	if !strings.Contains(err.Error(), "invalid login URL") {
		t.Errorf("error message missing context: %v", err)
	}
}
