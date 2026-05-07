package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Facets-cloud/praxis-cli/internal/credentials"
)

func resetUseFlags() { useJSON = false }

func TestUseCmd_SetsActiveProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetUseFlags()

	_ = credentials.Put("acme", credentials.Profile{URL: "https://acme.test", Token: "t"})

	var buf bytes.Buffer
	useCmd.SetOut(&buf)
	if err := useCmd.RunE(useCmd, []string{"acme"}); err != nil {
		t.Fatalf("RunE err = %v", err)
	}
	if !strings.Contains(buf.String(), `"active_profile": "acme"`) {
		t.Errorf("expected JSON active_profile: acme, got %q", buf.String())
	}

	// Verify ResolveActive now returns acme via SourceConfig.
	a, _ := credentials.ResolveActive("")
	if a.Name != "acme" || a.Source != credentials.SourceConfig {
		t.Errorf("after `use acme`, ResolveActive = %+v, want acme/config", a)
	}
}

func TestUseCmd_NonExistentProfile_Errors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PRAXIS_PROFILE", "")
	resetUseFlags()

	t.Skip("os.Exit(4) on missing profile; covered by manual + e2e testing")
}
