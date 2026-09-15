package cmd

import (
	"errors"
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
	"github.com/Facets-cloud/praxis-cli/internal/raptorinstall"
	"github.com/Facets-cloud/praxis-cli/internal/skillcatalog"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
)

// postAuthState captures what runPostAuthSetup did, for inclusion in
// the JSON output of `praxis login --json`. AI hosts read this to know
// exactly what changed on disk so they can decide whether to re-read
// any cached skill files.
type postAuthState struct {
	raptorBinary           raptorinstall.Result
	raptorSkills           []skillInstallationLite
	raptorWarning          string
	metaSkill              []skillInstallationLite
	removedSkills          []skillInstallationLite
	catalogSkills          []skillInstallationLite
	agents                 []agentInstallationLite
	removedAgents          []agentInstallationLite
	snapshotPath           string
	snapshotWarning        string
	skillWarning           string
	skillRecoveryDirectory string
	// hooksWired lists the host config files praxis wrote hooks into.
	hooksWired []string
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

// runPostAuthSetup installs the canonical skill, verifies local replacements,
// negotiates the catalog and stages its complete trees before retirement.
// Skill failures are reported without undoing saved credentials. Custom-agent,
// MCP snapshot, hook and freshness behavior remains independent and unchanged.
// The active state root determines both receipt and host install scope.
func runPostAuthSetup(out io.Writer, asJSON bool, baseURL string, auth map[string]string) postAuthState {
	state := postAuthState{}
	var raptorErr error
	state.raptorBinary, raptorErr = prepareRaptor(out, asJSON)
	if raptorErr != nil {
		state.raptorWarning = raptorErr.Error()
	}
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

	// Skills are serialized and committed as complete trees. A failed fetch
	// keeps the previous catalog; verified replacements retire exact globals.
	if !noHosts {
		raptorSkills, err := installRaptorSkills(hosts)
		state.raptorSkills = liteResults(raptorSkills)
		if err != nil {
			raptorErr = errors.Join(raptorErr, err)
			state.raptorWarning = raptorErr.Error()
			if !asJSON {
				fmt.Fprintf(out, "Warning: Raptor skill: %v\n", err)
			}
		}
		result := skillinstall.SyncCatalog(hosts, func(caps []string) ([]skillcatalog.Skill, error) {
			return fetchCatalog(baseURL, auth, caps...)
		})
		state.metaSkill = liteResults(result.Canonical)
		state.catalogSkills = liteResults(result.Installed)
		state.removedSkills = liteResults(result.Removed)
		state.skillRecoveryDirectory = skillinstall.RecoveryDirectory()
		if !asJSON && state.skillRecoveryDirectory != "" {
			fmt.Fprintf(out, "Preserved skill content/provenance (if migrated): %s\n", state.skillRecoveryDirectory)
		}
		if result.Err != nil {
			state.skillWarning = result.Err.Error()
			if !asJSON {
				fmt.Fprintf(out, "Warning: skill refresh: %v\n", result.Err)
			}
		}
	}

	// Step 3.5: agent catalog. Fetch first, then swap — same fail-safe as
	// skills. A transient network error leaves existing agents on disk.
	if !noHosts {
		agents, fetchErr := fetchAgents(baseURL, auth)
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
	state.snapshotPath, state.snapshotWarning = refreshMCPSnapshot(out, asJSON, baseURL, auth)

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

// wirePraxisHooks installs praxis's hooks into every detected host that has
// them (see claudehooks.Hosts). Paths are always USER-level, even under
// --local, because logout only cleans those. Never fatal: a wire failure warns.
func wirePraxisHooks(out io.Writer, asJSON bool, hosts []harness.Harness) []string {
	praxisPath, err := claudehooks.BinaryPath()
	if err != nil {
		if !asJSON {
			fmt.Fprintf(out, "Warning: could not resolve praxis binary for hook wiring: %v\n", err)
		}
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var wired []string
	for _, h := range hosts {
		host, ok := claudehooks.ByHarness(home, h.Name)
		if !ok || len(host.Events) == 0 {
			continue // no hook mechanism for this host
		}
		changed, err := claudehooks.Install(host, praxisPath)
		if err != nil {
			if !asJSON {
				fmt.Fprintf(out, "Warning: %s hooks not wired (skills installed; re-run to retry): %v\n", h.Name, err)
			}
			continue
		}
		wired = append(wired, host.File)
		if !asJSON {
			state := "wired"
			if !changed {
				state = "already wired"
			}
			fmt.Fprintf(out, "✓ %s: hooks %s → %s\n", h.Name, state, host.File)
			if h.Name == "codex" {
				fmt.Fprintln(out, "  run /hooks inside Codex once to trust it")
			}
		}
	}
	return wired
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

// refreshMCPSnapshot fetches the gateway's tool manifest and writes
// ~/.praxis/mcp-tools.json. Returns the destination path on success
// or an empty path + non-empty warning string when the snapshot
// could not be written (e.g. server too old to expose /v1/mcp/manifest).
// Either way the parent flow continues — a missing snapshot just means
// AI hosts fall back to live `praxis mcp` calls.
func refreshMCPSnapshot(out io.Writer, asJSON bool, baseURL string, auth map[string]string) (string, string) {
	raw, err := mcpmanifest.Fetch(baseURL, auth, mcpmanifest.DefaultTimeout)
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
