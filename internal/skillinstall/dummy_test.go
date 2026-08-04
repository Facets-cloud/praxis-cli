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
