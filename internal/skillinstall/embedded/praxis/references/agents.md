# Custom agents

An organization agent definition is not a running session, a local native agent
file, or a duty. `praxis agents --json` lists installed agent files; use deployed
`agent_ops` functions to inspect/manage server definitions.

```bash
praxis mcp agent_ops list_custom_agents --json
praxis mcp agent_ops get_custom_agent --arg agent_id=ID \
  --arg include_full_prompt=true --json
```

For a recurring domain, consider an existing agent or a duty before adding a
new permanent identity. If the user explicitly wants a new agent, establish its
charter and required access; do not silently widen an existing one instead.
One-off work does not itself call for a permanent agent.

Discover `create_custom_agent`, `update_custom_agent`, `delete_custom_agent` in
the manifest. Create supports name/display name, description, system prompt,
model, triggers, enabled system MCPs and optional attachments. Verify current
limits and allowed model/MCP values: unknown MCP identifiers can be dropped,
so inspect the saved definition and tools rather than trusting an accepted write.

Update only intended fields. Lists such as `triggers`, `enabled_system_mcps` and
`attached_custom_mcp_ids` replace the previous set, not append. Name/scope are not
rename fields. GLOBAL agents are read-only; ownership limits update/delete.
Deletion deactivates a definition, not an invitation to purge sessions or duties.

Do not grant extra tools, repository attachments or database access for convenience.
Treat prompt/attachment changes as behavior/access changes. Verify ID, ownership,
saved prompt/settings and resulting access. Then test with a scoped task if
execution is authorized. A saved agent is not a successful run. See
[Duties](duties.md) for recurring work and [Task DAG](task-dag.md) for gated work.
