# Repository access

Separate repository identity, access and permission to publish. A source checkout,
blueprint Git repository, module repository and generated Terraform export are
different objects. Discover the linked repo/branch; do not infer it from a service
name. Inspect `git status`, the remote identity (redacting embedded credentials),
base commit and repo instructions before editing. Preserve unrelated changes.

## Two access paths

- Ordinary local Git can use `praxis git-credential`, an HTTPS credential broker.
  It is Git's helper protocol, not a `praxis git clone` command. Let Git consume
  the short-lived token; never run helper `get` to display it. Credentials transit
  the local process even though they are centrally managed.
- Discover `vcs_cli.run_gh_cli` through the MCP manifest for server-side GitHub
  operations. Pass the explicit `repo` and command without `gh`/`--repo` according
  to its schema. It is not arbitrary shell execution or generic GitLab access.

Prefer an existing configured helper. If task-local configuration is needed,
scope it to the verified HTTPS host and preserve the repository path:

```bash
PRAXIS_PROFILE=PROFILE git \
  -c credential.https://github.com.helper= \
  -c 'credential.https://github.com.helper=!praxis git-credential' \
  -c credential.https://github.com.useHttpPath=true \
  ls-remote https://github.com/ORG/REPO.git refs/heads/BRANCH
```

Retain that scope for later authorized operations. Do not combine with a token
storing/caching helper, enable shell tracing, put tokens in URLs, or change an SSH
remote just to use this HTTPS route. Access denial does not authorize linking a
new VCS account. Request permission only where the workflow needs it; a review
request permits reads, not pushes, merges or PR updates.

## Bulk blueprint operations

Use the **raptor skill**, Git-first bulk blueprints route. Shared blueprint
values are Git-owned; environment overrides are control-plane-owned and are
**not** in that repo. Prepare one coherent source diff for bulk shared changes,
then validate and follow the authorized delivery/ingestion workflow. Do not
replace an asked-for Git/PR change with a loop of Raptor applies.

```text
Blueprint source diff -> tests -> delivery -> CP ingestion
Override changes --------------------------> effective environment config
Reviewed release --------------------------> deployed state
```

Git access does not authorize sync or deployment. A plan reads CP configuration,
not an un-ingested local branch. Repo edits, override edits and cloud effects have
separate rollback paths. Raptor owns artifact/service mapping and `$` expression
discovery; [Catalog onboarding](catalog-onboarding.md) uses that mapping to
associate source repositories, not to create CI artifacts.
