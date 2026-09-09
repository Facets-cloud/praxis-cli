# New Relic operations

Discover `newrelic_cli.list_newrelic_integrations`; select the account ID and
US/EU region as well as integration name. The gateway injects its credentials;
do not expose or override them. Run `run_newrelic_cli` with commands **without**
the `newrelic` prefix.

```bash
praxis mcp newrelic_cli run_newrelic_cli --json \
  --arg integration_name=OBS_ACCOUNT \
  --arg command="nrql query --query \"SELECT count(*) FROM TransactionError SINCE 1 hour ago FACET appName LIMIT 20\""
```

Discover the actual available schema and CLI help before using unfamiliar verbs.
Useful read areas: APM application list/get/search; entity search/get; NRQL over
Transaction, TransactionError, Log, Span; workloads, alert policies/conditions,
synthetics and workflows. Service names/GUIDs and cloud integration identities
are different: correlate them using evidence, not equal-looking labels.

For an incident, bound a shared time window, compare error **rates** and traffic,
then inspect error classes/logs and related infra. Low traffic can make absolute
counts misleading; missing telemetry is not zero errors. State NRQL limits,
sampling, retention and unvisited accounts.

Post-processing order is stdout -> `jq_expression` -> `grep_pattern` -> optional
`output_file`. Shell pipes are not allowed inside the remote command. File output
is on the server; request it only when an artifact/file is needed and confirm how
to retrieve it. [Gateway output](gateway-and-output.md) governs envelope parsing.

**Mutating subcommands are not blocked by the New Relic gateway's shell-safety
validator.** Deleting/changing alerts, dashboards, workflows or other objects
requires the user's change request and verified targets. Treat accepted writes
and operational success separately; read back the changed object. A diagnostic
request remains read-only even when the integration could execute writes.
