// Package agentcatalog fetches custom agents and standalone subagents
// from a Praxis deployment, then renders each one into the subagent
// file format that the local AI host (Claude Code, Gemini CLI, Codex)
// expects.
//
// Both /ai-api/custom-agents and /ai-api/subagents are queried; the
// CLI ignores admin-only fields (`is_active`, `can_edit`, etc.) and
// filters out inactive rows + agent-specific subagents (those with a
// non-empty parent_agent_name).
package agentcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// PrefixAgent is prepended to custom-agent names on disk so they
	// can't collide with user-authored or third-party agents.
	PrefixAgent = "praxis-"
	// PrefixSubagent is prepended to subagent names on disk; the
	// extra `sub-` segment makes the resource kind visually obvious
	// in `praxis agents` listings and prevents name collisions with
	// custom agents that happen to share a slug.
	PrefixSubagent = "praxis-sub-"

	// KindAgent / KindSubagent are the Agent.Kind values.
	KindAgent    = "agent"
	KindSubagent = "subagent"
)

// Agent is the slim CLI-side projection of either a CustomAgentResponse
// or a CustomSubagentResponse. Both endpoints return more fields than
// the CLI consumes — the rest are ignored at unmarshal time.
type Agent struct {
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	Icon         string `json:"icon,omitempty"`
	Model        string `json:"model"`
	Scope        string `json:"scope"`
	SystemPrompt string `json:"system_prompt"`
	IsActive     bool   `json:"is_active"`

	// ParentAgentName is set on subagents only. A non-empty value
	// means the subagent is scoped to a specific parent agent's
	// runtime — the CLI filters these out because there is no
	// parent runtime on the user's laptop.
	ParentAgentName string `json:"parent_agent_name,omitempty"`

	// Kind is set by Fetch() to "agent" or "subagent" depending
	// on which endpoint sourced the row. Not part of the wire
	// shape — server endpoints don't return it.
	Kind string `json:"-"`
}

// PrefixedName is the on-disk file basename for this agent. Different
// prefixes for the two kinds; subagent prefix is a strict subset of
// the agent prefix so `praxis-*` glob matching wipes both at once.
func (a Agent) PrefixedName() string {
	if a.Kind == KindSubagent {
		return PrefixSubagent + a.Name
	}
	return PrefixAgent + a.Name
}

const (
	customAgentsPath = "/ai-api/custom-agents"
	subagentsPath    = "/ai-api/subagents"
	defaultTimeout   = 30 * time.Second
)

// Fetch hits /ai-api/custom-agents and /ai-api/subagents in parallel,
// returning a merged + filtered + sorted slice of Agent records.
// Rows are filtered out client-side:
//   - is_active == false
//   - subagents with parent_agent_name != "" (agent-specific, not standalone)
//
// If EITHER endpoint errors, Fetch returns the error without installing
// any partial results — callers (login post-auth) leave existing agents
// in place on the user's disk when the catalog can't be loaded cleanly.
var Fetch = func(baseURL, token string) ([]Agent, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	var (
		wg           sync.WaitGroup
		agentsOut    []Agent
		subAgentsOut []Agent
		agentsErr    error
		subErr       error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		agentsOut, agentsErr = fetchOne(baseURL, token, customAgentsPath, KindAgent)
	}()
	go func() {
		defer wg.Done()
		subAgentsOut, subErr = fetchOne(baseURL, token, subagentsPath, KindSubagent)
	}()
	wg.Wait()

	if agentsErr != nil {
		return nil, fmt.Errorf("fetch custom-agents: %w", agentsErr)
	}
	if subErr != nil {
		return nil, fmt.Errorf("fetch subagents: %w", subErr)
	}

	merged := append(agentsOut, subAgentsOut...)
	out := merged[:0]
	for _, a := range merged {
		if !a.IsActive {
			continue
		}
		if a.Kind == KindSubagent && a.ParentAgentName != "" {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func fetchOne(baseURL, token, path, kind string) ([]Agent, error) {
	url := strings.TrimRight(baseURL, "/") + path
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
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, url, truncate(string(body), 200))
	}

	var raw []Agent
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	for i := range raw {
		raw[i].Kind = kind
	}
	return raw, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
