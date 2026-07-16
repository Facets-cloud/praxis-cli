package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/agentinstall"
	"github.com/Facets-cloud/praxis-cli/internal/claudehooks"
	"github.com/Facets-cloud/praxis-cli/internal/harness"
	"github.com/Facets-cloud/praxis-cli/internal/mcpmanifest"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
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
	agents          []agentInstallationLite
	removedAgents   []agentInstallationLite
	snapshotPath    string
	snapshotWarning string
	// hooksWired is the Claude settings.json path where the use-ig cwd hooks
	// were wired (SessionStart + CwdChanged), or "" when no claude-code host
	// was detected. The hooks nudge toward use-ig when cwd is an ig catalog
	// repo. AI hosts read this to know the nudge is live.
	hooksWired string
	// staleTools lists tools (praxis, raptor) found behind their latest release
	// at login, so the agent can offer an upgrade.
	staleTools []Freshness
	// projectScoped is the *effective* install scope after resolving the
	// active root — not the requested flag. It's false when a forced
	// project scope couldn't be enabled (e.g. cwd unresolvable or outside
	// home) and the install fell back to user-level, so callers report
	// where files actually landed.
	projectScoped bool
}

// skillInstallationLite is the trimmed JSON shape for skill installs in
// login output — drops the receipt-internal InstalledAt timestamp, whose
// churn would clutter output AI hosts re-read on every login.
type skillInstallationLite struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
}

// agentInstallationLite is the JSON shape used in login output. Mirrors
// skillInstallationLite for output consistency.
type agentInstallationLite struct {
	AgentName string `json:"agent_name"`
	Kind      string `json:"kind"`
	Harness   string `json:"harness"`
	Path      string `json:"path"`
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
//
// Install scope follows paths.ActiveRoot — the single source of truth, set up
// by the caller before calling this:
//
//   - `praxis login --local` and `praxis refresh-skills --project` pin the
//     active root to a project (<repo>/.praxis), so the install is
//     project-scoped: host skill dirs are rebased onto the project
//     (<repo>/.claude/skills, ...) and the receipt + MCP snapshot land in
//     <repo>/.praxis.
//   - `praxis login` (global) pins the active root to home, and a plain
//     `praxis refresh-skills` inside an active local-mode tree resolves to the
//     project automatically — both via ActiveRoot.
//   - Otherwise the install is user-level (~/.claude/skills, ~/.praxis).
//
// Because the receipt follows the same root as the install location, the
// "wipe previous profile" step (UninstallByPrefix) only ever touches the
// active root's receipt + that root's host dirs — so it runs unconditionally
// and stays safe in both scopes (a project refresh can't delete the user's
// global skills, and vice versa).
func runPostAuthSetup(out io.Writer, asJSON bool, baseURL, token string) postAuthState {
	state := postAuthState{}
	hosts := detectHarnesses()

	projectDir, inProject := resolveProjectScope()
	if inProject {
		for i := range hosts {
			hosts[i] = hosts[i].ProjectScoped(projectDir)
		}
		if !asJSON {
			fmt.Fprintf(out, "Installing project-scoped under %s\n", projectDir)
		}
	}
	state.projectScoped = inProject
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
			// The wipe targets the active root's receipt only (see func doc),
			// so it's safe in both user and project scope.
			removed := wipePrevProfileSkills(out, asJSON)
			state.removedSkills = liteResults(removed)
			orphaned := removeOrphanedProfileSkills(out, asJSON, nil, hosts)
			state.removedSkills = append(state.removedSkills, liteResults(orphaned)...)
			if !asJSON {
				fmt.Fprintln(out, "\nCatalog is empty for this org — nothing to install.")
			}
		default:
			// Catalog in hand. Now wipe and install. The wipe targets the
			// active root's receipt only (see func doc), so it's safe in
			// both user and project scope.
			removed := wipePrevProfileSkills(out, asJSON)
			state.removedSkills = liteResults(removed)
			orphaned := removeOrphanedProfileSkills(out, asJSON, skills, hosts)
			state.removedSkills = append(state.removedSkills, liteResults(orphaned)...)
			state.catalogSkills = installFetchedCatalog(out, asJSON, skills, hosts)
		}
	}

	// Step 3.5: agent catalog. Fetch first, then swap — same fail-safe as
	// skills. A transient network error leaves existing agents on disk.
	if !noHosts {
		agents, fetchErr := fetchAgents(baseURL, token)
		switch {
		case fetchErr != nil:
			if !asJSON {
				fmt.Fprintf(out, "\nWarning: agent catalog fetch failed: %v\n", fetchErr)
				fmt.Fprintln(out, "Existing agents left in place. Re-run `praxis login` once the gateway is reachable.")
			}
		default:
			// Agent wipe targets the active root's receipt only — same
			// reasoning as the skill wipe above (see func doc).
			removed, err := uninstallAgentsByPrefix("praxis-")
			if err != nil {
				if !asJSON {
					fmt.Fprintf(out, "Warning: removing previous profile's agents failed: %v\n", err)
				}
			}
			state.removedAgents = agentLiteResults(removed)
			if !asJSON && len(removed) > 0 {
				fmt.Fprintf(out, "\nRemoved %d agent file(s) from previous profile.\n", len(removed))
			}

			if len(agents) == 0 {
				if !asJSON {
					fmt.Fprintln(out, "\nAgent catalog is empty for this org — nothing to install.")
				}
			} else {
				installed, err := installAgents(agents, hosts)
				if err != nil {
					if !asJSON {
						fmt.Fprintf(out, "\nWarning: agent install failed: %v\n", err)
					}
				}
				state.agents = agentLiteResults(installed)
				if !asJSON {
					fmt.Fprintf(out, "\nInstalled %d agent file(s):\n", len(installed))
					for _, r := range installed {
						fmt.Fprintf(out, "  ✓ %-20s %-10s %s\n", r.AgentName, r.Kind, r.Path)
					}
				}
			}

			// Orphan cleanup: any praxis-* agent file in a detected
			// host's AgentDir that's NOT in the freshly-installed set
			// is a leftover (older praxis-cli version, gated host like
			// Codex still holding pre-gate files, etc.) — remove it.
			// Mirrors the catalog-skills orphan sweep above.
			keep := make(map[string]bool, len(agents))
			for _, a := range agents {
				keep[a.PrefixedName()] = true
			}
			orphaned, orphErr := agentinstall.RemoveOrphanedByPrefix("praxis-", hosts, keep)
			if orphErr != nil && !asJSON {
				fmt.Fprintf(out, "Warning: removing orphaned agent files failed: %v\n", orphErr)
			}
			if len(orphaned) > 0 {
				state.removedAgents = append(state.removedAgents, agentLiteResults(orphaned)...)
				if !asJSON {
					fmt.Fprintf(out, "Removed %d orphaned agent file(s).\n", len(orphaned))
				}
			}
		}
	}

	// Step 4: refresh MCP tools snapshot. Host-independent — useful even
	// without an AI host installed (manifest is consumed by other tools
	// and by future `praxis mcp` calls).
	state.snapshotPath, state.snapshotWarning = refreshMCPSnapshot(out, asJSON, baseURL, token)

	// Step 5: wire the use-ig cwd hooks (claude-code only). Never fatal — a
	// failed wire must not fail login; skills still installed above.
	if !noHosts {
		state.hooksWired = wirePraxisHooks(out, asJSON, hosts)
	}

	// Step 6: tool-freshness notice (praxis + raptor) via the shared engine.
	// Login already does network, so a live-if-stale check here also warms the
	// cache for later `praxis status` reads. Best-effort; never fatal.
	state.staleTools = noticeFreshness(out, asJSON)

	return state
}

// noticeFreshness checks tool freshness (concurrently + bounded, so an offline
// login isn't stalled by slow release lookups) and, for each tool behind its
// latest release, prints a one-line notice (non-JSON) and collects it. Uses the
// shared engine (freshCachedOrFetch), so it warms the cache too.
func noticeFreshness(out io.Writer, asJSON bool) []Freshness {
	var stale []Freshness
	for _, f := range checkToolsBounded(time.Now(), freshCachedOrFetch) {
		if !f.Stale {
			continue
		}
		stale = append(stale, f)
		if !asJSON {
			fmt.Fprintf(out, "! %s %s is behind %s — %s\n", f.Tool, f.Current, f.Latest, nagActionForTool(f.Tool))
		}
	}
	return stale
}

// wirePraxisHooks installs praxis's SessionStart + CwdChanged hooks into the
// claude-code host's USER-level settings.json so a session inside an ig catalog
// repo gets nudged toward the use-ig skill (see internal/claudehooks +
// `praxis ig hook`). Only claude-code has a settings.json hook mechanism; other
// hosts get skills but no hook. Returns the settings path on a successful wire,
// "" otherwise. Never fatal: a wire failure warns and returns "".
//
// The settings path is ALWAYS user-level, even under `--local` project scope:
// the hook resolves the active profile per-cwd at run time, so a single
// user-level hook serves every project — and logout (which unwires the
// user-level path) can then always clean it up. Deriving the path from the
// possibly-project-scoped `hosts` entry would strand the hook in a project's
// settings.json that logout never touches.
func wirePraxisHooks(out io.Writer, asJSON bool, hosts []harness.Harness) string {
	detected := false
	for _, h := range hosts {
		if h.Name == "claude-code" {
			detected = true
			break
		}
	}
	if !detected {
		return ""
	}
	cc, ok := harness.ByName("claude-code") // fresh, unscoped (user-level) harness
	if !ok {
		return ""
	}
	praxisPath, err := os.Executable()
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "Warning: could not resolve praxis binary for hook wiring: %v\n", err)
		}
		return ""
	}
	if resolved, rErr := filepath.EvalSymlinks(praxisPath); rErr == nil {
		praxisPath = resolved
	}
	settingsPath := filepath.Join(filepath.Dir(cc.SkillDir), "settings.json")
	changed, err := claudehooks.Install(settingsPath, praxisPath)
	switch {
	case err != nil:
		if !asJSON {
			fmt.Fprintf(out, "Warning: use-ig hooks not wired (skills installed; wire by hand or re-run): %v\n", err)
		}
		return ""
	case changed && !asJSON:
		fmt.Fprintf(out, "✓ claude-code: wired SessionStart + CwdChanged hooks → %s\n", settingsPath)
		fmt.Fprintln(out, "  (they nudge toward use-ig when cwd is an ig catalog repo)")
	case !changed && !asJSON:
		fmt.Fprintf(out, "✓ claude-code: use-ig hooks already wired → %s\n", settingsPath)
	}
	return settingsPath
}

// resolveProjectScope reads the already-resolved active root and reports the
// install scope. The active root is the single source of truth: a project
// root means project scope (host skill dirs rebased onto the dir containing
// .praxis); the home root means user scope. Callers (login --local,
// refresh-skills --project, or an active local-mode cwd) set the active root
// up front, so this never makes a scope decision on its own — keeping the
// receipt location (ActiveRoot) and the install location in lockstep.
func resolveProjectScope() (string, bool) {
	root, err := paths.ActiveRoot()
	if err != nil {
		return "", false
	}
	home, err := paths.Dir()
	if err != nil || root == home {
		return "", false
	}
	return filepath.Dir(root), true
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
		var results []skillinstall.Installation
		var err error
		if sk.IsMultiFile() {
			// Multi-file skill: write SKILL.md (with preamble) + supporting
			// files as a directory tree. Supporting files install verbatim —
			// the server already branded them; no preamble (SKILL.md-only).
			files := make([]skillinstall.FileBody, len(sk.Files))
			for i, f := range sk.Files {
				files[i] = skillinstall.FileBody{Path: f.Path, Content: f.Content}
			}
			results, err = installSkillTree(prefixed, sk.RenderedContent(), files, hosts)
		} else {
			results, err = installSkillBody(prefixed, sk.RenderedContent(), hosts)
		}
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

func agentLiteResults(in []skillinstall.AgentInstallation) []agentInstallationLite {
	out := make([]agentInstallationLite, 0, len(in))
	for _, r := range in {
		out = append(out, agentInstallationLite{
			AgentName: r.AgentName,
			Kind:      r.Kind,
			Harness:   r.Harness,
			Path:      r.Path,
		})
	}
	return out
}
