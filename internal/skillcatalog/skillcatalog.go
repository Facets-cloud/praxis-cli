// Package skillcatalog fetches the praxis skill catalog from a
// Praxis deployment.
//
// The skill bundle endpoint (`GET /ai-api/v1/skills/bundle`) returns
// every skill the authenticated API key can see — global, organization,
// and the caller's personal skills — each with full markdown content.
// The praxis CLI uses this to install org-authored skills into local
// AI hosts under the `praxis-<name>` namespace convention.
package skillcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// PraxisPrefix is prepended to org-skill names when written to disk.
	// `~/.claude/skills/praxis-<name>/SKILL.md` keeps provenance visible
	// and lets `praxis uninstall-skill --catalog` glob-match cleanly.
	PraxisPrefix = "praxis-"

	// bundlePath is the server endpoint that returns the full catalog.
	bundlePath = "/ai-api/v1/skills/bundle"

	defaultTimeout = 30 * time.Second
)

// Skill is the wire shape returned by /v1/skills/bundle. Mirrors the
// server's SkillResponse minus fields the CLI doesn't need.
type Skill struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Triggers    []string `json:"triggers"`
	Scope       string   `json:"scope"` // global | organization | personal
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Content     string   `json:"content"`
}

// PrefixedName is the on-disk skill folder name (e.g. praxis-incident-investigator).
// Skills the CLI installs from the catalog always carry this prefix so they
// can't collide with user-authored or third-party skills.
func (s Skill) PrefixedName() string {
	return PraxisPrefix + s.Name
}

// executionPreamble is inserted after the YAML frontmatter when a skill
// is installed onto a local AI host. Skill authors write tool references
// assuming in-process MCP transport ("call run_k8s_cli", "the cloud_cli
// MCP server", etc.); the local AI host doesn't have those tools — only
// the `praxis` CLI as a subprocess. The preamble teaches Claude the
// rewrite once, so the original skill body never has to change.
//
// Kept terse on purpose — Claude is good at applying rules from a
// short rationale + one concrete example. Long preambles burn context
// in every conversation that loads the skill.
const executionPreamble = "" +
	"> **Execution context** — this skill was authored for in-process MCP\n" +
	"> in agent-factory. You're running it from a local AI host installed by\n" +
	"> `praxis install-skill`, so MCP tools are NOT directly callable here.\n" +
	"> Whenever this skill references an MCP tool, shell out to `praxis`:\n" +
	">\n" +
	"> ```\n" +
	"> # Skill says:    run_k8s_cli(integration_name=\"prod\", command=\"get pods\")\n" +
	"> # You run:       praxis mcp k8s_cli run_k8s_cli \\\n" +
	">                  --arg integration_name=prod --arg command='get pods'\n" +
	"> ```\n" +
	">\n" +
	"> Rewrite rule: any `<mcp>.<fn>(args)` or bare `<fn>` reference becomes\n" +
	"> `praxis mcp <mcp> <fn> --arg k=v ...` (or `--body '<json>'` for nested\n" +
	"> args). The CLI authenticates as your Praxis user and runs the call\n" +
	"> server-side under your org's managed cloud / k8s credentials — your\n" +
	"> laptop never holds AWS / kube / terraform secrets.\n" +
	">\n" +
	"> If `praxis mcp <mcp> <fn>` returns 404, that tool isn't yet exposed\n" +
	"> by the gateway; fall back to whatever non-MCP path the skill suggests.\n" +
	">\n" +
	"> **Discovering what's available** — to see every MCP and function the\n" +
	"> gateway exposes, run `praxis mcp --json` (live fetch). A snapshot\n" +
	"> from your last `praxis install-skill` / `praxis refresh-skills` lives\n" +
	"> at `~/.praxis/mcp-tools.json` — grep that file when you need the\n" +
	"> tool list without making a network call.\n"

// RenderedContent is the SKILL.md body actually written to disk on a
// local AI host: content with a valid frontmatter block at the top and
// executionPreamble inserted just after that frontmatter. If the server
// sends body-only markdown, Praxis synthesizes minimum Agent Skills
// frontmatter so Codex and other loaders can still load the file.
func (s Skill) RenderedContent() string {
	return insertAfterFrontmatter(s.Content, executionPreamble, s.defaultFrontmatter(), s.PrefixedName())
}

// insertAfterFrontmatter splits a markdown document at the closing
// `---` of its YAML frontmatter and inserts `extra` (plus a blank line)
// between frontmatter and body. Documents without valid frontmatter get
// `fallbackFrontmatter` prepended first.
func insertAfterFrontmatter(body, extra, fallbackFrontmatter, expectedName string) string {
	body = strings.TrimLeft(body, "\n")
	const open = "---\n"
	if !strings.HasPrefix(body, open) {
		return fallbackFrontmatter + "\n" + extra + "\n" + body
	}
	rest := body[len(open):]
	// Closing fence is a `---` line — match either "\n---\n" or
	// "\n---" at end of doc.
	idx := strings.Index(rest, "\n---\n")
	endLen := len("\n---\n")
	if idx < 0 {
		// Tolerate trailing fence at very end of file (no final newline).
		idx = strings.Index(rest, "\n---")
		if idx < 0 || idx+4 != len(rest) {
			// Malformed frontmatter — make the file loadable and leave the
			// original bytes in the markdown body for human inspection. The
			// resulting file has two leading `---` blocks: the synth one we
			// prepend (parsed as frontmatter) and the broken original (now
			// inside the body). Frontmatter loaders only consume the leading
			// fence-delimited block, and a bare `---` line in markdown body
			// is a CommonMark thematic break — so this remains loadable by
			// Codex / Claude / Gemini skill scanners.
			return fallbackFrontmatter + "\n" + extra + "\n" + body
		}
		endLen = len("\n---")
	}
	frontmatterEnd := len(open) + idx + endLen
	frontmatter := ensureFrontmatterName(body[:frontmatterEnd], expectedName)
	return frontmatter + "\n" + extra + "\n" + strings.TrimLeft(body[frontmatterEnd:], "\n")
}

func ensureFrontmatterName(frontmatter, expectedName string) string {
	hasFinalNewline := strings.HasSuffix(frontmatter, "\n")
	trimmed := strings.TrimSuffix(frontmatter, "\n")
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return frontmatter
	}

	nameLine := "name: " + yamlString(expectedName)
	for i := 1; i < len(lines)-1; i++ {
		if strings.TrimLeft(lines[i], " \t") != lines[i] {
			continue
		}
		if strings.HasPrefix(lines[i], "name:") {
			if lines[i] == nameLine {
				// Already in the exact form we'd emit — skip the rewrite so
				// we don't churn quoting (e.g. `name: praxis-x` → `name: "praxis-x"`)
				// on every render of an already-correct file.
				return frontmatter
			}
			lines[i] = nameLine
			out := strings.Join(lines, "\n")
			if hasFinalNewline {
				out += "\n"
			}
			return out
		}
	}

	lines = append(lines[:1], append([]string{nameLine}, lines[1:]...)...)
	out := strings.Join(lines, "\n")
	if hasFinalNewline {
		out += "\n"
	}
	return out
}

func (s Skill) defaultFrontmatter() string {
	description := strings.TrimSpace(s.Description)
	if description == "" {
		description = strings.TrimSpace(s.DisplayName)
	}
	if description == "" {
		description = "Praxis catalog skill " + s.PrefixedName()
	}
	return fmt.Sprintf(
		"---\nname: %s\ndescription: %s\n---\n",
		yamlString(s.PrefixedName()),
		yamlString(description),
	)
}

func yamlString(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// Fetch is the HTTP seam — tests swap it to avoid hitting the network.
var Fetch = func(baseURL, token string) ([]Skill, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	url := strings.TrimRight(baseURL, "/") + bundlePath
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: defaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"HTTP %d from %s: %s",
			resp.StatusCode,
			url,
			truncate(string(body), 200),
		)
	}

	var skills []Skill
	if err := json.Unmarshal(body, &skills); err != nil {
		return nil, fmt.Errorf("parse bundle: %w", err)
	}
	return skills, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
