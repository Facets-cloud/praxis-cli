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
	// One shared store, two selectors; the meta-skill must teach the status
	// `raptor` block and the per-command FACETS_PROFILE prefix.
	for _, want := range []string{
		"## Raptor profile vs praxis profile",
		"~/.facets/credentials",
		"FACETS_PROFILE=<shared_profile> raptor",
		"prefix_required",
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

// Switching profiles used to require `praxis login --profile X`, which needs a
// human to click "Create Key" in a browser — so an agent asked to work against
// another deployment had no unattended path and would stall. `profiles use`
// reuses the stored token, so the meta-skill must name it (and the exit-3
// fallback) or hosts keep reaching for the browser flow.
func TestPraxisMetaSkill_TeachesProfileSwitching(t *testing.T) {
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatalf("ContentFor(praxis): %v", err)
	}

	for _, want := range []string{
		"praxis profiles use",         // the command itself
		"praxis profiles [--refresh]", // how to find out what's available
		"previous_profile",            // fields to read back
		"shadowed_by_project_root",
		"--local", // the per-repo alternative
	} {
		if !strings.Contains(body, want) {
			t.Errorf("meta-skill should mention %q so a host can switch profiles without a browser", want)
		}
	}

	// The old advice, as the ONLY way to switch, sends the host to a browser
	// flow it can't complete alone. It may appear solely as the exit-3 fallback.
	if strings.Contains(body, "active profile becomes acme") && !strings.Contains(body, "praxis profiles use acme") {
		t.Error("meta-skill still teaches login --profile as the way to switch profiles")
	}
}

// multiProfileMachine renders the meta-skill the way a user who has actually
// authenticated more than one profile sees it. Without this the doctrine is
// gated out, which is the point of the gate — so every assertion about it has
// to say which machine it is talking about.
func multiProfileMachine(t *testing.T) {
	t.Helper()
	prev := MultiProfileMachine
	MultiProfileMachine = func() bool { return true }
	t.Cleanup(func() { MultiProfileMachine = prev })
}

// A one-off read against another deployment should cost nothing: -p resolves
// for that invocation only. Without this in the meta-skill a host reaches for
// `profiles use` — wiping and reinstalling ~90 skill files — just to answer
// one question, and then leaves the user on the wrong profile.
func TestPraxisMetaSkill_TeachesOneOffProfileFlag(t *testing.T) {
	multiProfileMachine(t)
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatalf("ContentFor(praxis): %v", err)
	}

	for _, want := range []string{
		"-p <name>",  // the flag
		"praxis -p ", // at least one runnable example
		"exit 2",     // and where it doesn't work
	} {
		if !strings.Contains(body, want) {
			t.Errorf("meta-skill should mention %q so a host uses the cheap path for one-off reads", want)
		}
	}

	// All three refusing commands must be named, or a host will burn an
	// invocation discovering the exit code.
	for _, refuses := range []string{"logout", "refresh-skills", "profiles use"} {
		if !strings.Contains(body, refuses) {
			t.Errorf("meta-skill should name %q as refusing --profile", refuses)
		}
	}
}

// Several agent sessions on one machine is the normal case, and `profiles use`
// is machine-global: it repoints every session AND rewrites skill files those
// sessions have already read. A host that doesn't know to scope itself with
// PRAXIS_PROFILE will silently break its siblings.
func TestPraxisMetaSkill_TeachesSessionScopingForConcurrency(t *testing.T) {
	multiProfileMachine(t)
	body, err := ContentFor("praxis")
	if err != nil {
		t.Fatalf("ContentFor(praxis): %v", err)
	}

	for _, want := range []string{
		"PRAXIS_PROFILE",         // the mechanism
		"export PRAXIS_PROFILE=", // how to actually set it
		"MACHINE-GLOBAL",         // what profiles use really does
		"agent session",          // who gets hurt
		"scope yourself",         // the instruction itself
		"shadowed_by_env",        // how to read the result
	} {
		if !strings.Contains(body, want) {
			t.Errorf("meta-skill should mention %q so concurrent sessions don't clobber each other", want)
		}
	}

	// The honest caveat: routing follows the flag/env, skill FILES do not.
	if !strings.Contains(body, "SKILL FILES on disk always belong to") {
		t.Error("meta-skill must disclose that -p/env route commands but don't swap skill files")
	}
}

// All of the above is doctrine for a machine with several profiles. The typical
// customer has exactly one, so none of it describes a choice they can make —
// and shipping it anyway is what teaches their host to pass `-p` at the single
// profile it already resolves to. Gate it on the profile count.
func TestPraxisMetaSkill_MultiProfileDoctrineGatedOnProfileCount(t *testing.T) {
	single, err := ContentFor("praxis")
	if err != nil {
		t.Fatalf("ContentFor(praxis) single-profile: %v", err)
	}
	multiProfileMachine(t)
	multi, err := ContentFor("praxis")
	if err != nil {
		t.Fatalf("ContentFor(praxis) multi-profile: %v", err)
	}

	// Present for a multi-profile user, absent for a single-profile one. These
	// are the load-bearing lines of each gated subsection, so a future edit that
	// moves prose out of the gate trips here.
	for _, gated := range []string{
		"### Scope yourself instead of switching",
		"MACHINE-GLOBAL",
		"export PRAXIS_PROFILE=",
		"-p <name>",
		"SKILL FILES on disk always belong to",
		"exit 2",
	} {
		if strings.Contains(single, gated) {
			t.Errorf("single-profile meta-skill ships multi-profile doctrine %q", gated)
		}
		if !strings.Contains(multi, gated) {
			t.Errorf("multi-profile meta-skill lost %q", gated)
		}
	}

	// Everything a single-profile user still needs has to survive the cut —
	// including the local-mode section that sits AFTER the gated block, which is
	// what a bad cut would swallow.
	for _, always := range []string{
		"praxis profiles use",
		"praxis profiles rm NAME",
		"## Per-directory profiles (local mode)",
		"praxis profiles use acme --local",
	} {
		if !strings.Contains(single, always) {
			t.Errorf("single-profile meta-skill lost %q", always)
		}
		if !strings.Contains(multi, always) {
			t.Errorf("multi-profile meta-skill lost %q", always)
		}
	}

	// The cut is a const boundary, not a string search, but the two halves still
	// have to join into valid markdown: one blank line, no welded headings.
	const seam = "Only the catalog skills cycle.\n\n## Per-directory profiles (local mode)\n"
	if !strings.Contains(single, seam) {
		t.Error("single-profile body joins its two halves without a clean paragraph break")
	}
	if strings.Contains(single, "\n\n\n") || strings.Contains(multi, "\n\n\n") {
		t.Error("meta-skill body has a doubled blank line at a const boundary")
	}
}

// A name with no body errors at install time; a body with no name is never
// installed at all. Both are silent, so pin the pairing.
func TestSingleFileMetaSkills_EveryNameResolves(t *testing.T) {
	multiProfileMachine(t)
	for _, name := range singleFileMetaSkills {
		body, ok := metaSkillBody(name)
		if !ok {
			t.Errorf("%s is named in singleFileMetaSkills but has no body", name)
			continue
		}
		if !strings.HasPrefix(body, "---\nname: ") {
			t.Errorf("%s body doesn't open with skill frontmatter", name)
		}
		if !IsMetaSkill(name) {
			t.Errorf("%s resolves a body but IsMetaSkill says no — logout would wipe it", name)
		}
	}
}
