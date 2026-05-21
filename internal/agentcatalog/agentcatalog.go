// Package agentcatalog fetches custom agents from a Praxis deployment
// and renders each one into the subagent file format that the local
// AI host (Claude Code, Gemini CLI, Codex) expects.
//
// Only /ai-api/custom-agents is queried. (An earlier draft of this
// package also consumed /ai-api/subagents, but subagents are being
// removed server-side, so the CLI never shipped that consumption.)
// The CLI ignores admin-only fields (`is_active`, `can_edit`, etc.)
// and filters out inactive rows.
package agentcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Facets-cloud/praxis-cli/internal/render"
)

const (
	// PrefixAgent is prepended to custom-agent names on disk so they
	// can't collide with user-authored or third-party agents.
	PrefixAgent = "praxis-"

	// KindAgent is the only Agent.Kind value today. The Kind field
	// is kept on the Agent + receipt types for forward-compat in
	// case more resource types are introduced later — if so they'd
	// get their own KindXxx constant alongside this one.
	KindAgent = "agent"
)

// Agent is the slim CLI-side projection of a CustomAgentResponse.
// The server endpoint returns more fields than the CLI consumes —
// the rest are ignored at unmarshal time.
type Agent struct {
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	Icon         string `json:"icon,omitempty"`
	Model        string `json:"model"`
	Scope        string `json:"scope"`
	SystemPrompt string `json:"system_prompt"`
	IsActive     bool   `json:"is_active"`

	// Kind is set by Fetch() — always "agent" today. Retained as
	// a field for forward-compat with the receipt schema.
	Kind string `json:"-"`
}

// PrefixedName is the on-disk file basename for this agent.
func (a Agent) PrefixedName() string {
	return PrefixAgent + a.Name
}

const (
	customAgentsPath = "/ai-api/custom-agents"
	defaultTimeout   = 30 * time.Second
)

// Fetch hits /ai-api/custom-agents, filters inactive rows client-side,
// and returns a deterministically-sorted slice. On fetch failure the
// caller (login post-auth) leaves existing agents in place on disk —
// login must stay non-destructive across a flaky network.
//
// A 404 on /custom-agents is treated as "empty catalog" (the helper
// returns nil, nil) rather than an error, so deployments that don't
// expose the endpoint install nothing rather than failing login.
var Fetch = func(baseURL, token string) ([]Agent, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required")
	}
	if token == "" {
		return nil, fmt.Errorf("token is required")
	}

	agents, err := fetchOne(baseURL, token, customAgentsPath, KindAgent)
	if err != nil {
		return nil, fmt.Errorf("fetch custom-agents: %w", err)
	}

	out := agents[:0]
	for _, a := range agents {
		if !a.IsActive {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
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
	// 404 means this Praxis deployment doesn't expose the endpoint
	// (older server versions). Treat as "empty catalog" so login
	// installs nothing rather than failing. Auth and server-error
	// failures still bubble through to the caller.
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
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

// Render produces the on-disk file body for the given harness.
//
//   - claude-code / gemini-cli: YAML frontmatter + executionPreamble + system_prompt.
//   - codex: TOML with `developer_instructions` holding the same content
//     (preamble + system_prompt), wrapped in triple-quoted string literals.
//
// Returns an error for unknown harness names, and for Codex if the
// system_prompt contains the `"""` triple-quote sentinel (rare; the
// installer logs and skips that single agent).
func (a Agent) Render(harnessName string) (string, error) {
	descriptionForRender := a.Description
	if descriptionForRender == "" {
		descriptionForRender = a.DisplayName
	}
	if descriptionForRender == "" {
		descriptionForRender = "Praxis catalog " + a.PrefixedName()
	}

	body := render.ExecutionPreamble + "\n" + a.SystemPrompt

	switch harnessName {
	case "claude-code", "gemini-cli":
		return renderYAML(a.PrefixedName(), descriptionForRender, body), nil
	case "codex":
		return renderTOML(a.PrefixedName(), descriptionForRender, body)
	default:
		return "", fmt.Errorf("agentcatalog: unsupported harness %q", harnessName)
	}
}

func renderYAML(name, description, body string) string {
	return "---\n" +
		"name: " + yamlString(name) + "\n" +
		"description: " + yamlString(description) + "\n" +
		"---\n\n" +
		body + "\n"
}

func renderTOML(name, description, body string) (string, error) {
	if strings.Contains(body, `"""`) {
		return "", fmt.Errorf("agentcatalog: system_prompt for %q contains triple-quote sentinel; cannot render as Codex TOML", name)
	}
	return "name = " + tomlString(name) + "\n" +
		"description = " + tomlString(description) + "\n" +
		"developer_instructions = \"\"\"\n" +
		body + "\n" +
		"\"\"\"\n", nil
}

// yamlString encodes via json.Marshal for safe quoting in a YAML scalar
// context (JSON strings are a strict subset of YAML's double-quoted form).
func yamlString(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// tomlString applies TOML basic-string escaping — same rules as JSON for
// the characters we care about (quote, backslash, control chars).
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
