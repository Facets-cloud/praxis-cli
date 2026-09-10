# Optional unattended DAG runner

Use only when the user asks to keep an existing run moving unattended. Read
[Task DAG](task-dag.md) first. Use the host's supported recurring-task mechanism;
this pattern does not require Flow, a local daemon or a new custom agent.
The DAG server owns state; local journal entries are checkpoints, not authority.

Each bounded tick:

1. Read current run state. If terminal/aborted, retire this runner.
2. Peek with `next_node` for eligible auto nodes, then `own_node` up to the
   agreed concurrency/capacity. No packet means no claim; move on safely.
3. Execute owned packets, heartbeat leases, collect evidence, run the independent
   judge loop and complete per the packet schema. Do not re-claim another owner's
   node or replay completion after a lost response without reconciliation.
4. Check waiting gates/manual assignments. Notify the appropriate human once per
   meaningful state transition with a node UI link, not once per polling tick.
   Never approve/reject or take still-manual nodes on their behalf. A human may
   use “Unblock for AI”; only then may auto eligibility change.
5. Journal IDs, claimed/completed steps, waiting reason, last evidence and next
   check. Don't record credentials or entire secret-bearing tool results.

Self-pace according to server state: shorter checks during active work, longer
while waiting only on humans. Ten/45 minutes from the legacy recipe are examples,
not required timings. Lease heartbeats follow actual lease duration independently
of status polling. Bound errors/backoff; unchanged state is expected when waiting,
not a reason to create another run or escalate repeatedly.

The unattended mandate changes persistence, not authority. Any new destructive,
security-sensitive or out-of-scope action still requires the user's decision.
On shutdown, leave a resumable journal and report owned in-flight work; stopping
the local runner is not proof that remote work stopped.
