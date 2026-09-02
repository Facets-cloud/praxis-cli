package cmd

import (
	"fmt"
	"runtime"
	"slices"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/agentinstall"
	"github.com/Facets-cloud/praxis-cli/internal/credentials"
	"github.com/Facets-cloud/praxis-cli/internal/paths"
	"github.com/Facets-cloud/praxis-cli/internal/raptorstate"
	"github.com/Facets-cloud/praxis-cli/internal/render"
	"github.com/Facets-cloud/praxis-cli/internal/skillinstall"
	"github.com/spf13/cobra"
)

var (
	statusJSON    bool
	statusRefresh bool
	statusFull    bool
)

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "JSON output")
	statusCmd.Flags().BoolVar(&statusRefresh, "refresh", false,
		"live checks: verify the token via /ai-api/auth/me AND re-fetch tool (praxis/raptor) latest versions")
	statusCmd.Flags().BoolVar(&statusFull, "full", false,
		"include per-harness install detail (paths) in JSON output")
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show active profile, auth state, installed skills",
	Long: `Read-only snapshot for AI hosts to inspect: which profile is
active, whether it has credentials, and which skills are installed.

By default this is a LOCAL-ONLY snapshot (no network calls): the "tools"
freshness block is served from cache. Pass --refresh to make live calls —
/ai-api/auth/me (catches expired/revoked tokens; adds an "auth_check"
field) AND a re-fetch of each tool's latest release so "tools" reflects
current staleness.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		asJSON := render.UseJSON(statusJSON, false, out)

		active, _ := credentials.ResolveActive(rootProfile)
		skills, _ := skillinstall.List()
		agents, _ := agentinstall.List()
		loggedIn := active.Loaded && active.Profile.Token != ""

		state := map[string]any{
			"profile":        active.Name,
			"profile_source": active.Source,
			"url":            active.Profile.URL,
			"logged_in":      loggedIn,
			"username":       active.Profile.Username,
		}
		// Surface project (local) mode so AI hosts and users can see that
		// skills/receipt are scoped to this directory tree, not global.
		// Only when the active profile actually RESOLVED from the project
		// pointer — a bare/stray .praxis dir (or one whose pointer named a
		// profile we don't have) must not masquerade as local mode.
		projectRoot := ""
		if active.Source == credentials.SourceProject {
			if root, ok, _ := paths.ProjectRoot(); ok {
				projectRoot = root
				state["project_root"] = root
			}
		}

		// Tool freshness (praxis + raptor) from the shared engine. Plain status
		// is a local-only snapshot → cached latest, no network; --refresh forces
		// a live re-check alongside the token check below.
		freshnessMode := freshCached
		if statusRefresh {
			freshnessMode = freshLive
		}
		state["tools"] = toolsFreshness(time.Now(), freshnessMode)

		// Raptor auth state: which profile a BARE raptor command would use
		// here, and whether that is this praxis profile. The store is shared,
		// so a praxis profile that is a control-plane PAT is always usable by
		// raptor as FACETS_PROFILE=<name>; the block tells the AI host when
		// that prefix is needed.
		raptorSt := raptorstate.Resolve()
		state["raptor"] = raptorStatusBlock(raptorSt, active)

		// One field an AI host can branch on instead of re-deriving "is this
		// machine actually usable?" from installed/found/logged_in. The skills'
		// raptor preflight reads this.
		state["setup_complete"] = loggedIn && raptorReady(raptorSt, active)

		if asJSON {
			if statusFull {
				// Same shaped schema as `list-skills --json` and
				// `praxis agents --json` — the receipt structs (with
				// their internal timestamp) stay off the wire.
				state["skills_installed"] = toSkillOutputShape(skills)
				state["agents_installed"] = toAgentOutputShape(agents)
			} else {
				// Status is read at the start of every AI conversation —
				// keep it small. Names only; per-harness paths live behind
				// --full, `praxis agents --json`, and `list-skills --json`.
				skillNames, agentNames := summarizeInstalls(skills, agents)
				state["skills_installed"] = skillNames
				state["agents_installed"] = agentNames
			}
		}

		// --refresh: live token check via /auth/me. Folds in the
		// behavior of the (now deprecated) `whoami` command. Local-only
		// fields above are still returned even on auth-check failure.
		if statusRefresh && loggedIn {
			user, err := fetchAuthMe(active.Profile.URL, active.Profile.Auth())
			if err != nil {
				state["auth_check"] = map[string]any{
					"ok":    false,
					"error": err.Error(),
				}
				// Don't os.Exit here — status is read-only diagnostic
				// info, callers should branch on the JSON shape.
			} else {
				state["auth_check"] = map[string]any{
					"ok":       true,
					"username": user.Email,
					"user_id":  user.UserID,
				}
				// Update username from server in case it changed.
				state["username"] = user.Email
			}
		}

		if asJSON {
			return render.JSON(out, state)
		}

		fmt.Fprintf(out, "profile:    %s (source: %s)\n", active.Name, active.Source)
		if projectRoot != "" {
			fmt.Fprintf(out, "local mode: %s\n", projectRoot)
		}
		fmt.Fprintf(out, "url:        %s\n", active.Profile.URL)
		fmt.Fprintf(out, "raptor:     %s\n", raptorStatusLine(raptorSt, active))
		if loggedIn {
			fmt.Fprintf(out, "logged in:  yes (%s)\n", active.Profile.Username)
		} else {
			fmt.Fprintf(out, "logged in:  no — run `praxis login`\n")
		}
		if check, ok := state["auth_check"].(map[string]any); ok {
			if ok2, _ := check["ok"].(bool); ok2 {
				fmt.Fprintf(out, "auth check: ✓ token valid (%v)\n", check["username"])
			} else {
				fmt.Fprintf(out, "auth check: ✗ %v\n", check["error"])
			}
		}
		fmt.Fprintf(out, "skills:     %d installed\n", len(skills))
		for _, s := range skills {
			fmt.Fprintf(out, "  - %-30s %-12s @ %s\n", s.SkillName, s.Harness, s.Path)
		}
		fmt.Fprintf(out, "agents:     %d installed\n", len(agents))
		for _, a := range agents {
			fmt.Fprintf(out, "  - %-30s %-9s %-12s @ %s\n", a.AgentName, a.Kind, a.Harness, a.Path)
		}
		// Last thing on screen, so an unfinished setup isn't buried above the
		// skills/agents listings.
		fmt.Fprint(out, setupNotice(raptorSt, active))
		return nil
	},
}

// raptorAssetName is the release asset for a platform, or "" when raptor
// publishes no build for it. Names match the assets actually on
// Facets-cloud/raptor-releases (darwin/linux, amd64/arm64).
func raptorAssetName(goos, goarch string) string {
	switch goos {
	case "darwin", "linux":
	default:
		return ""
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return ""
	}
	return fmt.Sprintf("raptor-%s-%s", goos, goarch)
}

// raptorInstallHint points at raptor's own install instructions, plus an
// escape hatch for hosts that can't use them.
//
// `docs` is the primary answer. raptor owns its install steps and we must not
// fork them into praxis — that README already drifts from reality (it documents
// Windows binaries the releases don't publish), and a second copy here would
// drift further. Those documented steps end in `sudo mv … /usr/local/bin`.
//
// `no_sudo_commands` is the hatch: `sudo` prompts for a password, which a
// non-interactive AI host cannot answer, so it would hang rather than fail.
// The hatch installs to ~/.local/bin instead. That deviates from the README on
// purpose, and the note says so — ~/.local/bin is not on every PATH.
//
// praxis is the only party that knows this machine's OS/arch, so it resolves
// the asset; skill text can't.
func raptorInstallHint(goos, goarch string) map[string]any {
	hint := map[string]any{"docs": raptorInstallURL}
	asset := raptorAssetName(goos, goarch)
	if asset == "" {
		// No published build for this platform — docs only. Never fabricate a
		// download URL that 404s, and offer no hatch we can't stand behind.
		hint["note"] = "raptor publishes no build for this platform; follow docs."
		return hint
	}
	url := raptorDownloadURL + asset
	hint["asset_url"] = url
	hint["no_sudo_commands"] = []string{
		"mkdir -p ~/.local/bin",
		"curl -fsSL " + url + " -o ~/.local/bin/raptor",
		"chmod +x ~/.local/bin/raptor",
	}
	hint["note"] = "Prefer docs — raptor's own steps install to /usr/local/bin via sudo. " +
		"no_sudo_commands is an escape hatch for non-interactive hosts that can't answer a " +
		"sudo password prompt; it installs to ~/.local/bin, which must be on PATH."
	return hint
}

// raptorStatusBlock shapes a raptorstate.State for JSON output. `installed`
// and `found` are always present; resolution detail only when it exists, and
// the praxis-URL comparison only when a control plane actually resolved.
func raptorStatusBlock(st raptorstate.State, active credentials.Active) map[string]any {
	return raptorStatusBlockFor(st, active, runtime.GOOS, runtime.GOARCH)
}

// raptorStatusBlockFor is raptorStatusBlock with the platform injected so the
// install hint is testable across OS/arch.
//
// `shared_profile` is set when the active praxis profile is a control-plane
// PAT in the shared store — usable by raptor as FACETS_PROFILE=<name>.
// `prefix_required` says a bare raptor command would hit a DIFFERENT control
// plane (or none), so the host must prefix every raptor command with
// FACETS_PROFILE=<shared_profile>. Same host under another section name
// needs no prefix.
func raptorStatusBlockFor(st raptorstate.State, active credentials.Active, goos, goarch string) map[string]any {
	block := map[string]any{
		"installed": st.Installed,
		"found":     st.Found,
	}
	if !st.Installed {
		block["install_hint"] = raptorInstallHint(goos, goarch)
	}
	if st.Profile != "" {
		block["profile"] = st.Profile
	}
	if st.Source != "" {
		block["source"] = string(st.Source)
	}
	if st.Found {
		block["control_plane_url"] = st.ControlPlaneURL
		if st.Username != "" {
			block["username"] = st.Username
		}
		block["matches_praxis_url"] = raptorstate.MatchesHost(active.Profile.URL, st.ControlPlaneURL)
	}
	if sharedProfile(active) {
		block["shared_profile"] = active.Name
		block["prefix_required"] = prefixRequired(st, active)
	}
	return block
}

// prefixRequired reports whether a bare raptor command would miss this praxis
// profile's control plane, so FACETS_PROFILE=<name> must be set.
func prefixRequired(st raptorstate.State, active credentials.Active) bool {
	return !st.Found || !raptorstate.MatchesHost(active.Profile.URL, st.ControlPlaneURL)
}

// sharedProfile reports whether the active praxis profile is a control-plane
// PAT raptor can use (a section of the shared store, or the env override both
// CLIs read).
func sharedProfile(active credentials.Active) bool {
	return active.Loaded && (active.Profile.Store == credentials.StoreFacets || active.Profile.Store == credentials.StoreEnv)
}

const (
	// raptorInstallURL is raptor's OWN install instructions — the single place
	// those steps are maintained. praxis points at it rather than restating
	// them, so the two can't drift. raptor ships no Homebrew formula or cask
	// today (unlike praxis), so this README is the canonical path.
	raptorInstallURL = "https://github.com/Facets-cloud/raptor-releases#installation"

	// raptorDownloadURL is the release-asset prefix, used only to resolve the
	// exact build for this machine.
	raptorDownloadURL = "https://github.com/Facets-cloud/raptor-releases/releases/latest/download/"
)

// raptorStatusLine renders the human one-liner for the raptor auth state.
func raptorStatusLine(st raptorstate.State, active credentials.Active) string {
	switch {
	case st.Found:
		match := "no"
		if raptorstate.MatchesHost(active.Profile.URL, st.ControlPlaneURL) {
			match = "yes"
		}
		line := fmt.Sprintf("profile %s (%s) → %s (matches praxis url: %s)",
			st.Profile, st.Source, st.ControlPlaneURL, match)
		if sharedProfile(active) && prefixRequired(st, active) {
			line += fmt.Sprintf("; use FACETS_PROFILE=%s for this praxis profile", active.Name)
		}
		return line
	case sharedProfile(active):
		return fmt.Sprintf("no default profile — use FACETS_PROFILE=%s (shared with praxis)", active.Name)
	case st.Profile != "":
		// FACETS_PROFILE names a profile raptor doesn't have.
		return fmt.Sprintf("profile %q (%s) not found in ~/.facets/credentials", st.Profile, st.Source)
	case !st.Installed:
		// State the fact AND the next step. "not installed" alone names no
		// consequence, and nothing else in the repo tells a user where to get it.
		return "not installed — get it at " + raptorInstallURL
	default:
		return "no profile resolved — run `raptor login`"
	}
}

// raptorReady reports whether raptor can actually run a control-plane command:
// on PATH, and either resolved bare or usable through the shared praxis
// profile with a FACETS_PROFILE prefix.
func raptorReady(st raptorstate.State, active credentials.Active) bool {
	return st.Installed && (st.Found || sharedProfile(active))
}

// setupNotice is the closing summary printed when raptor isn't usable yet.
//
// Without it the per-field `raptor:` line is followed by `logged in: yes`, so
// the output as a whole still scans as healthy — a user has no reason to look
// closer. praxis login succeeding is only half of setup: raptor is what reaches
// projects, resources, environments and releases, so every praxis user needs it
// working. Returns "" when there is nothing to say.
func setupNotice(st raptorstate.State, active credentials.Active) string {
	if raptorReady(st, active) {
		return ""
	}
	if !st.Installed {
		return "\n⚠ setup incomplete: raptor is not installed.\n" +
			"  Facets projects, resources and releases all run through raptor.\n" +
			"  Install: " + raptorInstallURL + "\n" +
			"  Then:    raptor login\n"
	}
	// Installed but no usable profile — don't send them back to the install page.
	return "\n⚠ setup incomplete: raptor is installed but not logged in.\n" +
		"  Run: raptor login\n"
}

// summarizeInstalls collapses the per-(name, harness) receipt entries into
// deduped, sorted name lists. Slices are always non-nil so JSON marshals
// `[]`, never `null`.
func summarizeInstalls(
	skills []skillinstall.Installation,
	agents []skillinstall.AgentInstallation,
) (skillNames, agentNames []string) {
	skillNames = make([]string, 0, len(skills))
	for _, s := range skills {
		skillNames = append(skillNames, s.SkillName)
	}
	agentNames = make([]string, 0, len(agents))
	for _, a := range agents {
		agentNames = append(agentNames, a.AgentName)
	}
	slices.Sort(skillNames)
	slices.Sort(agentNames)
	return slices.Compact(skillNames), slices.Compact(agentNames)
}
