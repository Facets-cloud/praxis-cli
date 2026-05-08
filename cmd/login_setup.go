package cmd

import (
	"fmt"
	"io"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/mcpmanifest"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// postAuthState captures what runPostAuthSetup did, for inclusion in
// the JSON output of `praxis login --json`. AI hosts read this to know
// exactly what changed on disk so they can decide whether to re-read
// any cached skill files.
type postAuthState struct {
	metaSkill       []skillInstallationLite
	removedSkills   []skillInstallationLite
	catalogSkills   []skillInstallationLite
	snapshotPath    string
	snapshotWarning string
}

// runPostAuthSetup is the v0.7 invariant-keeper that runs after
// credentials are saved + the active pointer is flipped:
//
//  1. Install the meta-skill into every detected AI host (idempotent).
//  2. Wipe any praxis-* org skills from the previous profile.
//  3. Fetch this profile's skill catalog and install each entry as
//     praxis-<name> across all detected AI hosts.
//  4. Refresh ~/.praxis/mcp-tools.json from the server's MCP manifest.
//
// Each step is best-effort: a failure logs a warning to `out` but does
// not roll back the credentials save. The user can re-run login any
// time to retry the steps that failed (login is idempotent).
func runPostAuthSetup(out io.Writer, asJSON bool, baseURL, token string) postAuthState {
	state := postAuthState{}
	hosts := detectHarnesses()
	if len(hosts) == 0 {
		if !asJSON {
			fmt.Fprintln(out, "No supported AI hosts detected on this machine.")
			fmt.Fprintln(out, "Install Claude Code, Codex, or Gemini CLI first, then re-run.")
		}
		return state
	}

	// Step 1: meta-skill (idempotent — Install upserts).
	metaResults, err := installSkill(skillName, hosts)
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "Warning: meta-skill install failed: %v\n", err)
		}
	} else {
		if !asJSON {
			fmt.Fprintf(out, "Meta-skill installed into %d host(s):\n", len(metaResults))
			for _, r := range metaResults {
				fmt.Fprintf(out, "  ✓ %-12s @ %s\n", r.Harness, r.Path)
			}
		}
		state.metaSkill = liteResults(metaResults)
	}

	// Step 2: wipe any previous-profile org skills (praxis-* prefix).
	removed, err := skillinstall.UninstallByPrefix("praxis-")
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "Warning: removing previous profile's skills failed: %v\n", err)
		}
	} else if len(removed) > 0 {
		state.removedSkills = liteResults(removed)
		if !asJSON {
			fmt.Fprintf(out, "\nRemoved %d skill(s) from previous profile.\n", len(removed))
		}
	}

	// Step 3: fetch the new profile's catalog and install each as praxis-<n>.
	catalogResults := installCatalogForHosts(out, asJSON, baseURL, token, hosts)
	state.catalogSkills = catalogResults

	// Step 4: refresh MCP tools snapshot.
	state.snapshotPath, state.snapshotWarning = refreshMCPSnapshot(out, asJSON, baseURL, token)

	return state
}

// installCatalogForHosts fetches the skill bundle and installs each
// entry as a praxis-prefixed skill across the given hosts. Returns a
// flat list of installations (one entry per host per skill).
//
// Empty catalog → empty list, no error. Fetch failure → empty list +
// stderr warning. Per-skill install failure → logged, batch continues.
func installCatalogForHosts(out io.Writer, asJSON bool, baseURL, token string, hosts []harness.Harness) []skillInstallationLite {
	skills, err := fetchCatalog(baseURL, token)
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "\nWarning: catalog fetch failed: %v\n", err)
			fmt.Fprintln(out, "Re-run `praxis login` once the gateway is reachable.")
		}
		return nil
	}
	if len(skills) == 0 {
		if !asJSON {
			fmt.Fprintln(out, "\nCatalog is empty for this org — nothing to install.")
		}
		return nil
	}

	if !asJSON {
		fmt.Fprintf(out, "\nInstalling %d catalog skill(s) as praxis-<n>:\n", len(skills))
	}

	all := make([]skillInstallationLite, 0, len(skills)*len(hosts))
	failures := 0
	for _, sk := range skills {
		prefixed := sk.PrefixedName()
		results, err := installSkillBody(prefixed, sk.RenderedContent(), hosts)
		if err != nil {
			if !asJSON {
				fmt.Fprintf(out, "  ✗ %-40s failed: %v\n", prefixed, err)
			}
			failures++
			continue
		}
		for _, in := range results {
			if !asJSON {
				fmt.Fprintf(out, "  ✓ %-40s → %s\n", prefixed, in.Path)
			}
			all = append(all, skillInstallationLite{Harness: in.Harness, Path: in.Path})
		}
	}
	if !asJSON && failures > 0 {
		fmt.Fprintf(out, "\n%d catalog skill(s) failed to install.\n", failures)
	}
	return all
}

// refreshMCPSnapshot fetches the gateway's tool manifest and writes
// ~/.praxis/mcp-tools.json. Returns the destination path on success
// or an empty path + non-empty warning string when the snapshot
// could not be written (e.g. server too old to expose /v1/mcp/manifest).
// Either way the parent flow continues — a missing snapshot just means
// AI hosts fall back to live `praxis mcp` calls.
func refreshMCPSnapshot(out io.Writer, asJSON bool, baseURL, token string) (string, string) {
	raw, err := mcpmanifest.Fetch(baseURL, token, mcpmanifest.DefaultTimeout)
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "\nMCP tool snapshot skipped: %v\n", err)
		}
		return "", err.Error()
	}
	dest, err := mcpmanifest.WriteSnapshot(raw)
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "\nMCP tool snapshot skipped: %v\n", err)
		}
		return "", err.Error()
	}
	if !asJSON {
		fmt.Fprintf(out, "\nMCP tool snapshot written to %s\n", dest)
	}
	return dest, ""
}

// liteResults trims InstalledAt out of skillinstall.Installation
// records, matching the shape that the rest of cmd uses for JSON
// output (init.go's skillInstallationLite).
func liteResults(in []skillinstall.Installation) []skillInstallationLite {
	out := make([]skillInstallationLite, 0, len(in))
	for _, r := range in {
		out = append(out, skillInstallationLite{Harness: r.Harness, Path: r.Path})
	}
	return out
}
