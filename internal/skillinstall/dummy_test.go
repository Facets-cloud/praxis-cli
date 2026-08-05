package skillinstall

import (
	"strings"
	"testing"
)

// The `praxis` meta-skill is the first thing a host loads on any infra
// request, so it is where the model forms its "how do I reach Facets?"
// prior. raptor was removed from the gateway (agent-factory#1382) and now
// runs as a LOCAL CLI; the meta-skill must steer control-plane reads to
// `raptor`, not to a non-existent `raptor_cli` MCP namespace. Regression
// guard for the bug where the model burned several `praxis mcp` discovery
// calls hunting for a list-projects tool before finding `raptor get projects`.
func TestPraxisMetaSkill_RaptorIsLocalNotGateway(t *testing.T) {
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatalf("ContentFor(praxis): %v", err)
	}

	// Must teach that raptor is a local CLI for control-plane objects, with
	// the concrete command the model previously failed to reach for.
	for _, want := range []string{
		"raptor get projects",
		"raptor whoami",
		"raptor login",
		"`raptor_cli` gateway tool",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("meta-skill should mention %q so the model routes control-plane reads to the local raptor CLI", want)
		}
	}

	// Must NOT advertise raptor_cli as a live gateway MCP namespace — that
	// stale claim is what sent the model looking for it via `praxis mcp`.
	if strings.Contains(body, "`catalog_ops`, `raptor_cli`") {
		t.Error("meta-skill still lists `raptor_cli` as a gateway MCP namespace; it was removed from the gateway and is a local CLI")
	}

	// Must teach the freshness step: check status tools, offer `raptor upgrade`,
	// ask first (nudge-only).
	for _, want := range []string{"raptor upgrade", "stale", "ask first"} {
		if !strings.Contains(body, want) {
			t.Errorf("meta-skill missing raptor-freshness guidance %q", want)
		}
	}
	// `tools` is a JSON array, so the object path `raptor.stale` is wrong.
	if strings.Contains(body, "raptor.stale") {
		t.Error("meta-skill uses the wrong shape `raptor.stale`; tools is an array — find the raptor entry")
	}
}

func TestPraxisMetaSkill_RaptorProfileCrossCheck(t *testing.T) {
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatal(err)
	}
	// The stores are independent; the meta-skill must teach the status
	// `raptor` block, the pin, and the per-command FACETS_PROFILE prefix.
	for _, want := range []string{
		"## Raptor profile ≠ praxis profile",
		"~/.facets/credentials",
		"FACETS_PROFILE=<profile> raptor",
		"--raptor-profile",
		"matches_praxis_url",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("praxis meta-skill missing raptor-profile guidance %q", want)
		}
	}
}

func TestPraxisMetaSkill_ExplainsLocalMode(t *testing.T) {
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Per-directory profiles (local mode)",
		"praxis login --profile acme --local",
		"praxis refresh-skills --project",
		".praxis/config.json",
		"project_root",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("praxis meta-skill missing local-mode guidance %q", want)
		}
	}
}

func TestPraxisMetaSkill_ProfileManagementSurface(t *testing.T) {
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"praxis profiles",
		"praxis profiles rename OLD NEW",
		"praxis profiles rm NAME",
		"praxis login --dry-run",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("praxis meta-skill missing profile-management surface %q", want)
		}
	}
}

// #68 taught the meta-skill to act on the `raptor` block, but only covered a
// raptor that is present and mis-aimed. A raptor that is absent, or present
// and logged out, left the host with nothing runnable — the old text said
// "ask the user to install it" and no instructions existed anywhere.
//
// The host now installs and signs raptor in on the user's behalf, matching how
// it already treats `praxis login`. Credentials stay off-limits either way.
func TestPraxisMetaSkill_RaptorSetupIsActionable(t *testing.T) {
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatalf("ContentFor(praxis): %v", err)
	}
	for _, want := range []string{
		"installed: false", // the case #68 didn't cover
		"install_hint",     // where the resolved commands live
		"~/.local/bin",     // no-sudo install target
		"setup_complete",   // the single field to branch on
	} {
		if !strings.Contains(body, want) {
			t.Errorf("meta-skill missing raptor-setup guidance %q", want)
		}
	}

	// The old stance told the host to stop at asking. That dead-ended the user.
	if strings.Contains(body, "don't install it yourself") {
		t.Error("stale guidance: the host now installs raptor via install_hint")
	}

	// Handling raptor's PAT is still forbidden — installing and running a
	// browser login is not the same as touching credentials.
	for _, want := range []string{
		"Never ask for a token in chat",
		"never write `~/.facets/credentials` yourself",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("credential guardrail weakened — missing %q", want)
		}
	}
}
