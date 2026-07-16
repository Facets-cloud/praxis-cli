# Spec: Judge-Gated Task DAG for praxis CLI + agent-factory

**Status:** draft for review · 16 Jul 2026
**Task:** task-dag (agent-factory-enhancements)
**Companion artifacts:** design doc (🕸️ d824356e…), operating model (🎛️ 633f5bf2…)

## 1. Summary

A complex task (PRD → feature → PR → deploy/rollback; cloud migration) becomes a
DAG stored durably in the agent-factory control plane. Nodes are units of work
with briefs, success criteria, and typed results. Edges are handover contracts.
An independent judge — a fresh local Claude subagent — assesses each node's
evidence against its criteria; only a passing verdict (or human approval)
unblocks downstream nodes. All Claude sessions run on user laptops; the server
never touches a model. Claude, not humans, operates the entire lifecycle.

## 2. Locked decisions

| # | Decision | Choice |
|---|----------|--------|
| D1 | Substrate | Server-side: agent-factory owns DAG state; praxis CLI is a client |
| D2 | Authoring | v1 curated templates; v2 planner agent proposes graphs, human approves; same format |
| D3 | Judge type | Per-node: `agent` \| `human` \| `agent_then_human`; template defaults |
| D4 | Architecture | Path A (server state machine + stateless laptop runner) + conductor-on-A |
| D5 | Workers | Laptop-only. Every Claude session (worker/judge/runner/conductor) on user laptops |
| D6 | Operator | Claude triggers every `praxis dag` command; humans express intent + approve gates |
| D7 | Worker runtime | flow: nodes = `flow do --auto` tasks; unattended loop = flow owner; bare `claude -p` fallback for flow-less laptops |
| D8 | Judge runtime | Local-only: fresh headless subagent per verdict (`claude -p` / `agy -p`), bridge-orchestrated inner converge loop; server has zero model access |
| D9 | Judge tools | Per-node `judge_mode`: `evidence` (no tools) or `verify` (read-only tools — fetch PR, run tests, kubectl get) |
| D10 | Evidence | Submit envelope includes full redacted transcript by default, ACL-gated server-side |
| D11 | Claim granularity | Claim = session contract: 1 claim = 1 attempt = 1 fresh session; nodes are the collaboration surface; `claim_mode: auto\|manual` + `assignee_hint` (§4a) |
| D12 | Agent-agnostic UX | The user only ever operates a coding agent (claude/codex/agy). Thin per-harness skill (~1 page: "call `praxis dag …`"), thick praxis CLI (all orchestration logic). DAG nuances never surface in conversation unless asked |

## 3. Data model (Mongo, `*_model.py` + `*_store.py` pairs)

### TaskGraphTemplate (versioned definition)
```yaml
template_id: cloud-migration
version: 3
name: "Cloud migration (AWS→GCP)"
params_schema: { ...JSON Schema... }        # e.g. customer, source, target
nodes:
  - node_id: write-tf-modules
    title: "Port Terraform modules"
    brief_template: |                        # param-substituted at instantiation
      Port the modules for {{customer}} ...
    output_schema: { ...JSON Schema... }     # worker must emit result.json matching this
    success_criteria:                        # judge-visible, checkable statements
      - "PR exists and CI is green"
      - "plan shows zero destructive changes"
    judge: agent            # agent | human | agent_then_human
    judge_mode: verify      # evidence | verify (read-only tools)
    max_iterations: 3       # inner converge loop cap
    retry_policy: { max_attempts: 2 }        # outer (re-claim) attempts
    reversible: true
    claim_mode: auto        # auto (runner-owner claims) | manual (waits for a
                            #   person's session to claim deliberately)
    assignee_hint: sre      # optional role/person routing for manual nodes
    worker_hints: { needs_vpn: customer-x }  # capability routing
edges:
  - { from: write-tf-modules, to: plan-migration,
      payload_contract: { pr_url: str, module_path: str, spec_digest: str },
      kind: normal }        # normal | rollback
```

### TaskGraphRun (live state)
- `run_id`, `template_id@version`, `params`, `status: running|paused|completed|aborted`
- `initiated_by: { principal, agent_context }`  — human principal, Claude actor
- `node_runs[]`: `node_id`, `state`, `claim {by, lease_expires_at, attempt_token}`,
  `attempts[] {result, summary, artifacts[], verdict, iteration_trail[], provenance}`,
  timestamps
- `verdict_log[]`, `event_log[]` — append-only audit
- Artifacts: `{type: pr_url|file|tfplan|log|transcript, uri, sha256, size, uploaded_by}`;
  transcripts redacted on-laptop before upload, stored ACL-gated (role-visible only)

## 4. Node state machine (all transitions server-side)

```
pending → ready → claimed → executing → submitted → passed | failed | needs_human
```
- `pending→ready`: all upstream `normal` edges' nodes are `passed`.
- `ready→claimed`: atomic CAS (`find_one_and_update`), lease (default 30m),
  heartbeats extend; expiry → back to `ready`. Stale attempt tokens → 409.

### 4a. Claim = session contract (collaboration model)

The node is the unit of claim, and **the claim IS the session**:

- **1 claim = 1 attempt = 1 Claude session.** Claiming materializes a flow
  task and binds exactly one fresh session to it. The session's logical
  lifecycle is the node attempt: claim → work → inner converge loop (same
  session, `--with` continuations) → submit → `flow done` (KB sweep,
  transcript upload) → session archived. No session ever spans two nodes;
  no session lingers past its node.
- **Outer retry = new claim = new session.** A re-claimed node's fresh
  session inherits the prior attempt's summary + judge feedback FROM THE
  SERVER (attempts[] record) — never the dead session's context. No session
  resurrection across claims.
- **Collaboration is claim-level, not run-level.** Any laptop's session may
  claim any ready node it's eligible for; the CAS makes concurrent claims
  safe. Different nodes of one run are routinely worked by different
  people's sessions in parallel — the run is a shared board, `dag status`
  the team view. `dag adopt` ensures a runner-owner exists but does not
  grant run ownership.
- **`claim_mode` routes autonomy vs. people.** `auto` nodes are claimed by
  runner-owner ticks (headless `flow do --auto` sessions). `manual` nodes
  are never auto-claimed: they wait for a person to say "I'll take X" —
  their ambient Claude claims it and works it interactively with the human
  steering. `assignee_hint` routes manual nodes to a role/person; claims
  are stamped `claimed_by: {principal, session_id, mode}`.
- `executing→submitted`: one submit per node with the full envelope (§6).
- `submitted→passed`: server validates output_schema + attempt token + verdict
  schema; `judge=agent` + verdict PASS → passed. `agent_then_human` → parks
  `needs_human` (verdict recommends; human confirms via push notification —
  question task + ask-mac / app UI).
- `submitted→failed`: local convergence exhausted (max_iterations without PASS);
  full iteration trail attached; human decides retry / edit criteria / abort.
- Rollback: `kind: rollback` edges traversed only by explicit
  `praxis dag rollback --to <node>` after human decision; re-opens target +
  descendants.

## 5. Judge: local inner converge loop

Per completed worker session, the **bridge** (deterministic praxis CLI code — no
model in the enforcement path) runs:

1. Fetch judge packet from server: criteria, edge contract, artifact refs,
   attempt #. (The judge-packet API structurally never returns transcripts.)
2. Spawn fresh judge subagent — any headless agent CLI returning JSON
   (`claude -p`, `agy -p`). Prompt = criteria + evidence, assembled by bridge.
   `judge_mode: verify` grants read-only tools to check reality.
3. Verdict `{pass, checks[], missing[], confidence}`.
   - FAIL → feedback re-enters the SAME warm worker session:
     `flow do --auto <slug> --with "judge: missing …"`. Iterate.
   - PASS or max_iterations → submit once.

Invariants:
- Worker never spawns the judge, never writes its prompt, never sees it.
- Judge context is empty per iteration; sees criteria vs evidence only.
- No model ever assembles a prompt for another model in the enforcement path.

## 6. Submit envelope

`POST /runs/{run}/nodes/{node}/submit`:
`attempt_token`, `result` (output_schema-valid), `summary {did, decisions[],
deviations[]}` (shipping note, not reasoning), `artifacts[]` (evidence refs +
uploads), `verdict` (final), `iteration_trail[]` (every inner round),
`judge_prompt` + `prompt_digest`, `provenance {principal, laptop, session ids,
flow slug, judge_cli+model, timings, tokens}`, `transcript` (full, redacted,
→ ACL-gated store).

Never consumed by enforcement: transcript, reasoning. Principle: the server
stores claims, evidence, and judgment — enforcement never consumes cognition.

## 7. Interface surface (machine-first)

All verbs: `--json`, non-interactive, idempotent (idempotency keys on `start`,
attempt tokens on `submit`). Shipped simultaneously as praxis CLI verbs + MCP
gateway functions (`routes/cli_gateway.py` pattern, API-key auth, audited).
The `praxis-dag` / `praxis-dag-runner` skills ARE the interface spec.

```
dag templates                       list/inspect templates
dag start <template> --param …      instantiate run (idempotency key)
dag adopt <run>                     ensure runner-owner exists for run
dag claim / heartbeat / submit      worker protocol
dag status <run>                    graph + node states (rendered conversationally)
dag approve|reject <run> <node>     relays a human's click
dag rollback --to <node>            explicit, human-decided
dag pause|abort <run>
```

## 8. Runner-owner lifecycle (flow)

One flow owner per active run (`dag-runner:<run-id>`), created by `dag adopt`.
The owner claims only `claim_mode: auto` nodes — `manual` nodes belong to
people's sessions (§4a); the owner's job for those is noticing they're ready
and nudging the assignee (question task / ask-mac), never claiming them.
Each tick: claim ready auto nodes (respect `-j`, worker_hints) → materialize flow
tasks (`--tag dag:<run> --tag node:<id>`, brief = work packet) → dispatch
`flow do --auto` → for completed sessions run the inner converge loop → submit
→ handle `needs_human` (question task + notify) → self-pace → journal → exit.
`auto_run: dead` → stop heartbeats → lease expiry re-readies node server-side.
Run completed/paused → owner retires itself.

Side effect: every node closes via `flow done` → KB sweep captures learnings
per node.

Fallback (no flow on laptop): bridge spawns bare `claude -p`, babysits PIDs,
no KB sweep, no owner mode.

## 9. Knowledge distribution (layered ignorance)

| Context | Knows | Via |
|---|---|---|
| Ambient session | templates exist; `dag start/adopt` | `praxis-dag` skill |
| Runner-owner tick | claim→converge→submit loop | owner charter + `praxis-dag-runner` skill |
| Node session | NOTHING about DAGs | flow brief = work packet |
| Judge subagent | NOTHING about DAGs | bridge-assembled prompt |
| Conductor (optional) | full protocol + graph state | `praxis-dag` skill + `dag status` |

**Thin skill, thick CLI (D12).** All intelligence — bridge orchestration,
claim/lease protocol, judge prompt assembly, converge loop, flow integration,
work-packet→brief generation, redaction, submit envelope — lives in the praxis
CLI, implemented once. The per-harness instruction layer is ~1 page ("when the
user describes a complex multi-step task, use `praxis dag …`; verbs and
when-to-use"): a proper skill for Claude Code, an AGENTS.md block for codex, a
prompt file for agy. Distribution rides the existing `cli_skills` channel.

## 10. Reuse map (agent-factory)

- Judge loop shape: `entities/alert_doctor/cycle_runner.py` (actor/critic,
  `JudgeFeedback`) — generalized; runs laptop-side now.
- Verify-against-reality: `entities/alert_doctor/confirmer.py` pattern →
  `judge_mode: verify`.
- Stores: `agent_schedule_model` / `agent_scheduled_run_model` two-model
  pattern → `task_graph_model` / `task_graph_run_model`; indexes via
  `stores/index_manager.py`; migrations in `migrations/`.
- CLI lane: `routes/cli_gateway.py` + `cli_gateway_audit_store`.
- Human gates: `anthropic_agents/permission_handler.py` (WS approvals) +
  notification channels; ask-mac laptop-side.
- Lease sweeps: `anthropic_agents/agent_scheduler.py` (APScheduler tick).
- NOT used for execution: `orchestrator/` k8s pods, `ExternalAgentRunner`
  (future server-worker option only; D5 makes laptop-only a policy, not a
  constraint — the claim/submit protocol is worker-agnostic).

## 11. Generalization for Claude

The three ecosystem gaps this fills (none exist in Workflow tool / Agent SDK /
LangGraph-as-a-service form): durable cross-session DAG store; independent
judge as first-class edge gate; human gates mid-graph. Because the worker
protocol is REST + JSON Schema, any agent runtime can participate (Claude Code
session, Agent SDK app, GH Action, human). The format is portable beyond
praxis.

## 12. v1 / v2 cutline

**v1:** template CRUD (curated YAML), instantiation, state machine, claim/lease
protocol, local judge + converge loop, submit envelope + transcript store,
runner-owner + `praxis-dag`/`praxis-dag-runner` skills, needs_human push gates,
`dag status`, rollback verb, audit logs.

**v2:** planner agent (task → proposed DAG, human approves; same format),
conductor UX (`dag conduct` interactive worker; graph-edit proposals), template
marketplace/versioning UX, org blob store for artifacts, server-worker type
(if ever needed), offline sync (only if demanded).

## 13. Security / authz

- API-key auth on all verbs (existing cli_gateway); per-principal audit.
- Attempt tokens prevent stale double-applies; leases prevent zombie claims.
- Transcripts: laptop-side redaction pass (secret patterns) before upload;
  ACL-gated collection; judge-packet API never returns them.
- Trust model: laptops are trusted actors (they hold prod credentials today);
  the judge defends against model self-deception, not malicious operators.
- Irreversible nodes (`reversible: false`): template must set `judge: human`
  or `agent_then_human`; server rejects templates that violate this rule.

## 14. Open questions (deferred, non-blocking)

- Judge model tier policy (judge on Opus while worker on Sonnet?) — template
  hint vs org default. (Min tier per user policy: Sonnet.)
- Artifact blob storage backend (server GridFS vs org S3/GCS) — v1 can start
  with refs + small uploads.
- Concurrency cap per laptop (`-j`) defaults and per-run caps.
- Cross-run dependencies (a run depending on another run's node) — out of
  scope v1.
