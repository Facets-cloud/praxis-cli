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
	"> by the gateway; fall back to whatever non-MCP path the skill suggests.\n"

// RenderedContent is the SKILL.md body actually written to disk on a
// local AI host: original content with executionPreamble inserted just
// after the YAML frontmatter (or at the top, if the skill has none).
// The original body is preserved byte-for-byte.
func (s Skill) RenderedContent() string {
	return insertAfterFrontmatter(s.Content, executionPreamble)
}

// insertAfterFrontmatter splits a markdown document at the closing
// `---` of its YAML frontmatter and inserts `extra` (plus a blank line)
// between frontmatter and body. Documents without frontmatter get the
// extra block prepended at the top.
func insertAfterFrontmatter(body, extra string) string {
	body = strings.TrimLeft(body, "\n")
	const open = "---\n"
	if !strings.HasPrefix(body, open) {
		return extra + "\n" + body
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
			// Malformed frontmatter — bail gracefully and prepend.
			return extra + "\n" + body
		}
		endLen = len("\n---")
	}
	frontmatterEnd := len(open) + idx + endLen
	return body[:frontmatterEnd] + "\n" + extra + "\n" + strings.TrimLeft(body[frontmatterEnd:], "\n")
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
	defer resp.Body.Close()
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
