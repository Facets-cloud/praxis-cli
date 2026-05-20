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
