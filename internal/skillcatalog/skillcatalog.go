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
