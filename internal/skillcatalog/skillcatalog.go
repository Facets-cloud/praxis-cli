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

	"github.com/Facets-cloud/praxis-cli/internal/render"
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

// RenderedContent is the SKILL.md body actually written to disk on a
// local AI host: content with a valid frontmatter block at the top and
// render.ExecutionPreamble inserted just after that frontmatter. If the server
// sends body-only markdown, Praxis synthesizes minimum Agent Skills
// frontmatter so Codex and other loaders can still load the file.
func (s Skill) RenderedContent() string {
	return insertAfterFrontmatter(s.Content, render.ExecutionPreamble, s.defaultFrontmatter(), s.PrefixedName())
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
