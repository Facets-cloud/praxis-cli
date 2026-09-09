# Memory

Praxis memory is server-side knowledge scoped by identity and audience. Native
host memory is a separate local system; neither replaces the other. Use recall
when organization context matters, not for every coding question. Treat remembered
facts as dated evidence and verify live state before changing infrastructure.

```bash
praxis memory recall "release ownership" --json
praxis memory list --tag infra --limit 100 --offset 0 --json
```

Recall is keyword-ranked, not semantic/exhaustive: default 5, maximum 20 results.
If terms miss a relevant fact, try better terms or a filtered list. List includes
content and is paginated (default/maximum 100); advance `--offset` when needed.
Do not call the first page “everything”. These commands emit JSON; `--json` is
accepted for consistency.

Save durable conventions, decisions, ownership and references only when the user
asks or accepts a concrete proposal to remember them. Never persist inferred
customer facts as established truth. State scope, source and date where useful;
omit credentials and sensitive operational payloads.

`praxis memory add` requires title and content; `--content -` reads stdin.
Kinds are `user`, `feedback`, `project`, `reference`; importance and tags support
discovery. `--audience user` is the default: this caller's knowledge on this
deployment, across agents. `--audience org` broadens visibility to the organization.
Select that only with explicit intent to share; “organizational fact” does not
automatically mean “publish it org-wide”. A personal preference can stay in native
memory, or in user-audience Praxis memory if the user wants it portable.

Verify the returned saved identity/audience. On auth/network failure, report the
write as unconfirmed and reconcile before repeating it; do not claim “remembered”
based on a proposed command. Installed customer skills and memories may belong to
different profiles: [Context and access](context-and-access.md).
