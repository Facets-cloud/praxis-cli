# Documents, evidence and UI links

A Praxis artifact is a versioned document/evidence object. A **Facets CI artifact**
is an integration that registers builds and maps to service image fields; use
the **raptor skill** for that. Sharing the word “artifact” does not share the API.

Discover `praxis mcp artifacts` functions from the full MCP manifest. For an
authorized published document, `emit_artifact` takes a stable `name`, title,
format (`markdown` or `html`), content and optional description. Keep the same
name when revising one deliverable so versions remain one history. Do not publish
just because a read-only answer could be made into a report.

```bash
jq -n --rawfile content report.md \
  '{name:"change-audit",title:"Change audit",format:"markdown",content:$content}' \
  | praxis mcp artifacts emit_artifact --body - --json
```

Check exit status and returned payload before recording identity. Provide the
returned durable `url` for teammates; a `latest_url` scoped to `session_id` is a
same-session convenience, not a generally shareable link. Durable does not mean
public: permissions still apply. Use `list_artifacts`, `read_artifact_latest` or
`read_artifact_version` according to the actual deployed schema.

Task-DAG deliverables require `{artifact_id, sha256}` in designated result fields.
Hash **the exact stored content bytes** (including trailing newline), not a title
or locally reformatted rendering. The envelope distinguishes internal identity
refs from external HTTP links; see [Task DAG](task-dag.md). Do not invent
`artifact://` URLs or assume latest means the version a judge approved.

CLI versioning is scoped to its server-assigned synthetic session plus name,
not the name alone across an entire org. `read_artifact_latest` reads that CLI
identity's history. To read another agent's report, list org artifacts and use
its specific version ID. Read functions can truncate large bodies (currently
256 KB); don't hash truncated readback as the full stored content.

## Human navigation

Prefer returned canonical URLs. Otherwise use the verified selected console base
and a documented route, encoding path/query components. For DAGs:

```text
<base>/ui/ai/task-dag/
<base>/ui/ai/task-dag/runs/<run-id>
<base>/ui/ai/task-dag/runs/<run-id>?node=<node-id>
```

Supply the node link when a human must approve, unblock or take a step. For
Facets resources/releases/actions/module registry tabs use Raptor's UI navigation
reference; do not guess routes from CLI nouns. When UI inspection is necessary,
use an available authenticated browser capability; never carry tokens in URLs.
An absent browser does not block returning a verified link or finishing CLI work.
