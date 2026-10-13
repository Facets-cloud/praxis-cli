# Duties

A duty is a server-side recurring objective; its schedule, individual runs and
accumulated findings are separate objects. It is not a local launchd/cron/Flow job.
Do not create one merely because a diagnostic might benefit from monitoring.

## Read vs trigger

Native `praxis duty` commands are read-only: `list`, `runs`, `run`, `report`,
`findings`. **`praxis duty run RUN_ID` shows a run; it does not trigger a duty.**
Use each command's help for its selectors. Native runs default to 20 (max 100),
findings to 200 (max 1000); report limits before drawing historical conclusions.

Mutations use `praxis mcp duties`: discover `create_duty`, `update_duty`,
`pause_duty`, `resume_duty`, `delete_duty`, `trigger_duty`. Reads also exist there:
`list_duties`, `list_duty_runs`, `get_duty_run`, `list_duty_findings`.

```bash
praxis mcp duties trigger_duty --arg schedule_id=DUTY_ID --json
praxis mcp duties get_duty_run --arg run_id=RETURNED_RUN_ID --json
```

Trigger is asynchronous and runs even while the recurring schedule is paused.
Do not resume it just to run once. Optional `instructions` apply to that one run,
not persistent learning. Track the returned run ID to terminal outcome and read
findings/actions; acceptance is not completion. A lost response requires run
history reconciliation before a repeat trigger.

## Schedule lifecycle

Default agent is `praxis` when none is named. Create needs name, cron expression
and objective. Timezone defaults to **UTC**; pass an explicit IANA zone for a
human-local schedule. Include the intended targets and allowed effects in the
objective. Slack/Git integration overrides broaden external effects and need to
be part of the agreed task, not silent convenience defaults.

Update uses `schedule_id` plus changed fields. Changing cron/timezone re-registers
the schedule. Pause retains its history; resume also clears its error counter.
Delete is different from pause: inspect the exact duty before deletion.

After changes, read back enabled state, cron, timezone, target agent and objective;
verify next run where exposed. Inspect failures, not only findings: no findings
can mean no successful scan. Include duty/run IDs and an available UI link.

Hosted pods use differently named `agent_ops` schedule tools. Do not copy that
namespace into CLI recipes. Finding resolution is not assumed to be exposed in
CLI; discover it rather than inventing a native `duty resolve` command.
