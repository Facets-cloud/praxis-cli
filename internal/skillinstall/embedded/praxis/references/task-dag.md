# Task DAG

Use a durable DAG for requested multi-step/gated work spanning sessions or people.
A single-session edit does not need a new run. All operations are through
`praxis mcp task_dag FUNCTION`; there is **no native `praxis dag` command**.
Server templates and returned packets define the contract, not this example.

## Start and inspect

Discover `list_templates`, select a matching template and confirm material params
in the user's terms. Don't author/change shared templates merely because none
fits; request that additional scope. `create_run` takes template ID, params,
human-readable `display_name`, a stable idempotency key and optional version.
Persist the key across a retry of the same intended creation; a new key is a new
operation, not error recovery.

Read `list_runs`/`get_dag` for status. Give the verified console's run UI link:
`<base>/ui/ai/task-dag/runs/<run-id>`. For a blocked human gate add
`?node=<node-id>`. The link is the person's approval/pickup surface. Summarize
work done/waiting plainly; don't expose lease jargon unless it helps.

## Claim and execute

`next_node` only peeks at eligibility. `own_node` claims a node and returns a
work packet; a lost claim race can return no packet. Do not execute without
ownership. For a requested manual pickup, use its supported `mode: manual` and
session ID. Capability hints are string values such as `{"raptor":"true"}`,
not booleans; advertise only capabilities actually present.

```text
Packet: brief + validated inputs + output/envelope schemas + success criteria
                         |
                 owned attempt token + lease
                         |
                  work + evidence
                         |
               independent judge -> fixes
                         |
               complete -> server validation -> handoff / human gate
```

Use only the packet's assigned work and upstream payloads; ownership of a node
does not authorize unrelated live changes. Result fields must match `output_schema`
and the envelope must match `envelope_schema`. Long work must heartbeat with the
run/node/attempt token using packet lease defaults. An expired lease allows
another owner; re-read state rather than submitting stale results.

## Judge and completion

Fetch `judge_packet`. Give a **fresh independent judge** the criteria, result and
targeted artifact/evidence, not your conversation or rationale. It returns
`{pass, checks[], missing[], confidence}`. If independent judging is unavailable,
report that limit—do not self-grade as independent. No expensive/live operation
should be repeated solely to create judging evidence.

On failure, repair the missing items. Fresh judge per iteration; subsequent
inputs can focus on failed/disturbed checks plus prior verdict, changed evidence
and a concrete delta. Carry settled checks with provenance. Respect packet
`max_iterations`; don't loop until it rubber-stamps a pass.

Submit `complete_node` with the exact attempt token, result, summary, artifacts,
verdict, iteration trail, judge prompt/digest and provenance required by the
returned schema. A 409 or 422 is not a generic transient retry: inspect ownership/
gate/schema error and current DAG first. Correct a rejected payload only if that
attempt still owns the node and the server permits resubmission. A lost response
may already have completed: reconcile before a duplicate submission.

## Deliverables and gates

Packet `deliverables` fields hold `{artifact_id, sha256}`, not inline Markdown/HTML.
Emit the document using [Artifacts](artifacts-and-ui.md), hash exact content, then
reference it. Revise the same artifact name to create versions; pin the version
judged. Envelope evidence uses these shapes (plus any current schema requirements):

```json
{"type":"artifact","artifact_id":"ID","label":"Review evidence"}
{"type":"link","uri":"https://...","label":"PR / external evidence"}
```

Never invent an artifact URI. Internal artifact refs identify objects, external
links identify actual URLs. Format/hash/existence are validated server-side.

`approve`/`reject` express the user's decision on a named gate, not the agent's
preference. Rejection carries feedback into another attempt. Rollback is an
explicit user decision using the current `rollback` contract; it is not automatic
undo of every external side effect. Preserve manual assignments. For unattended
driving read [DAG runner](task-dag-runner.md).

When authorized to design templates, write judgeable criteria: traceable inputs,
observable output/evidence, explicit boundaries. Avoid arbitrary field counts,
unseen-process claims and undefined words such as “reasonable”. Don't embed the
desired verdict in the brief. Existing work-packet compatibility is authoritative.
