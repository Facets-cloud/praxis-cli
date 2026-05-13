package cmd

import (
	"fmt"
	"io"

	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/mcpmanifest"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
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
//     Skipped when no hosts are detected.
//  2. Fetch this profile's skill catalog from the server. If the fetch
//     fails, leave existing org skills in place — login stays
//     non-destructive. (CodeRabbit PR #3 #2: don't wipe before fetch.)
//  3. Once the catalog is in hand, wipe any praxis-* org skills from
//     the previous profile and install the new ones. The wipe-and-
//     install is one logical step so we never leave the user with no
//     org skills due to a transient network failure.
//  4. Refresh ~/.praxis/mcp-tools.json from the server's MCP manifest.
//     Runs even when no AI hosts are detected — a sysadmin or CI
//     pipeline may want the snapshot for tooling that doesn't go
//     through Claude/Codex/Gemini.
//
// Each step is best-effort: a failure logs a warning to `out` but does
// not roll back the credentials save. The user can re-run login any
// time to retry the steps that failed (login is idempotent).
func runPostAuthSetup(out io.Writer, asJSON bool, baseURL, token string) postAuthState {
	state := postAuthState{}
	hosts := detectHarnesses()
	noHosts := len(hosts) == 0
	if noHosts && !asJSON {
		fmt.Fprintln(out, "No supported AI hosts detected on this machine.")
		fmt.Fprintln(out, "Install Claude Code, Codex, or Gemini CLI to install skills.")
		fmt.Fprintln(out, "(Continuing — credentials and MCP manifest snapshot will still be written.)")
	}

	// Step 1: every binary-embedded meta-skill (idempotent — Install
	// upserts). Host-dependent. As of v0.x there are two:
	//   - "praxis"        the CLI driver
	//   - "praxis-memory" the memory-recall guide
	// Names come from MetaSkillNames() so adding another meta-skill
	// only requires a dummySkills entry.
	if !noHosts {
		for _, name := range skillinstall.MetaSkillNames() {
			metaResults, err := installSkill(name, hosts)
			if err != nil {
				if !asJSON {
					fmt.Fprintf(out, "Warning: meta-skill %q install failed: %v\n", name, err)
				}
				continue
			}
			if !asJSON {
				fmt.Fprintf(out, "Meta-skill %q installed into %d host(s):\n", name, len(metaResults))
				for _, r := range metaResults {
					fmt.Fprintf(out, "  ✓ %-12s @ %s\n", r.Harness, r.Path)
				}
			}
			state.metaSkill = append(state.metaSkill, liteResults(metaResults)...)
		}
	}

	// Step 2 + 3: fetch FIRST, then swap. If the fetch fails, leave the
	// existing org-skill set on disk — `praxis login` must be safe to
	// re-run on a flaky network without leaving the user empty-handed.
	// Host-dependent (no point fetching if we can't install).
	if !noHosts {
		skills, fetchErr := fetchCatalog(baseURL, token)
		switch {
		case fetchErr != nil:
			if !asJSON {
				fmt.Fprintf(out, "\nWarning: catalog fetch failed: %v\n", fetchErr)
				fmt.Fprintln(out, "Existing org skills left in place. Re-run `praxis login` once the gateway is reachable.")
			}
		case len(skills) == 0:
			// Empty catalog is a definitive answer — wipe stale entries.
			removed := wipePrevProfileSkills(out, asJSON)
			state.removedSkills = liteResults(removed)
			orphaned := removeOrphanedProfileSkills(out, asJSON, nil, hosts)
			state.removedSkills = append(state.removedSkills, liteResults(orphaned)...)
			if !asJSON {
				fmt.Fprintln(out, "\nCatalog is empty for this org — nothing to install.")
			}
		default:
			// Catalog in hand. Now wipe and install.
			removed := wipePrevProfileSkills(out, asJSON)
			state.removedSkills = liteResults(removed)
			orphaned := removeOrphanedProfileSkills(out, asJSON, skills, hosts)
			state.removedSkills = append(state.removedSkills, liteResults(orphaned)...)
			state.catalogSkills = installFetchedCatalog(out, asJSON, skills, hosts)
		}
	}

	// Step 4: refresh MCP tools snapshot. Host-independent — useful even
	// without an AI host installed (manifest is consumed by other tools
	// and by future `praxis mcp` calls).
	state.snapshotPath, state.snapshotWarning = refreshMCPSnapshot(out, asJSON, baseURL, token)

	return state
}

// wipePrevProfileSkills removes every praxis-* skill from disk and the
// receipt, returning the entries actually removed. The meta-skill
// ("praxis", no suffix) is preserved by UninstallByPrefix.
func wipePrevProfileSkills(out io.Writer, asJSON bool) []skillinstall.Installation {
	removed, err := skillinstall.UninstallByPrefix("praxis-")
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "Warning: removing previous profile's skills failed: %v\n", err)
		}
		return nil
	}
	if len(removed) > 0 && !asJSON {
		fmt.Fprintf(out, "\nRemoved %d skill(s) from previous profile.\n", len(removed))
	}
	return removed
}

// removeOrphanedProfileSkills removes stale on-disk praxis-* folders that
// are not in installed.json. These can be left behind by older Praxis
// builds or interrupted refreshes, and Codex will still try to load them.
func removeOrphanedProfileSkills(out io.Writer, asJSON bool, skills []skillcatalog.Skill, hosts []harness.Harness) []skillinstall.Installation {
	keep := make(map[string]bool, len(skills)+len(skillinstall.MetaSkillNames()))
	for _, name := range skillinstall.MetaSkillNames() {
		keep[name] = true
	}
	for _, sk := range skills {
		keep[sk.PrefixedName()] = true
	}

	removed, err := skillinstall.RemoveOrphanedByPrefix("praxis-", hosts, keep)
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "Warning: removing stale skill folders failed: %v\n", err)
		}
		return nil
	}
	if len(removed) > 0 && !asJSON {
		fmt.Fprintf(out, "Removed %d stale skill folder(s).\n", len(removed))
	}
	return removed
}

// installFetchedCatalog installs an already-fetched skill catalog. It
// does not fetch — the caller (runPostAuthSetup) does the fetch first
// so we can fail-safe on transient network errors. Returns a flat list
// of installations (one entry per host per skill).
//
// Per-skill install failure → logged, batch continues.
func installFetchedCatalog(out io.Writer, asJSON bool, skills []skillcatalog.Skill, hosts []harness.Harness) []skillInstallationLite {
	if len(skills) == 0 {
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
