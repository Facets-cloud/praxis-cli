---
name: "praxis-onboarding"
title: "Facets Onboarding Guide"
description: "Use when a new user wants to get started with Facets or Praxis hands-on, has a brand-new or empty control plane, or asks to 'onboard me', 'get me started', 'set up my first deployment', or 'resume onboarding'. Runs a guided journey, not a topic lookup."
triggers: ["onboard", "get started", "getting started", "first deployment", "resume onboarding"]
category: "education"
tags: ["onboarding", "getting-started", "guided", "hands-on", "first-deployment", "journey"]
icon: "🚀"
version: "1.0"
---

> **Execution context — two gateways.** Everything runs by shelling out to
> `praxis`. There are exactly two layers; do not confuse them:
>
> - **MCP tools = the gateway to Praxis.** Four servers — `catalog_ops`,
>   `cloud_cli`, `k8s_cli`, `raptor_cli` — each with named functions. Call
>   them as `praxis mcp <server> <function> --arg k=v` (or `--body '<json>'`).
>   List them with `praxis mcp --json` (snapshot: `~/.praxis/mcp-tools.json`).
>
>   ```
>   praxis mcp catalog_ops get_existing_catalog
>   praxis mcp raptor_cli list_facets_integrations
>   ```
>
> - **raptor = the gateway to the Facets control plane.** Reached through one
>   function on the `raptor_cli` server — `run_raptor_cli` — which runs any
>   raptor command against the linked CP (auth is the org/user PAT, set up at
>   `praxis login`; once a CP is PAT-linked this just works). Everything
>   inside Facets — projects, resources, environments, releases, **and
>   cloud-account linking** — is a raptor command:
>
>   ```
>   praxis mcp raptor_cli run_raptor_cli --arg command='get projects'
>   praxis mcp raptor_cli run_raptor_cli --arg command='get accounts'
>   ```
>
>   In this flow, raptor steps are written as the bare raptor command (e.g.
>   `raptor get projects`); always run it through the wrapper above.
>
> **Layer pitfall:** `praxis mcp cloud_cli list_cloud_integrations` lists
> *Praxis* cloud integrations (for running aws/gcloud CLI) — it is NOT where
> the Facets CP's linked clouds live. For clouds available to *deploy* into,
> ask Facets: `raptor get accounts`.
>
> **Discover, don't invent.** raptor verbs are real and specific. Before an
> unfamiliar one, run `raptor <noun> --help`.

# Facets Onboarding Guide

You run **guided onboarding journeys** that take a new user from a
brand-new (empty) control plane to genuine, hands-on understanding of
Facets. This is a *journey with a beginning and end* — not the reactive
topic lookup that `praxis-learning` provides. For deep dives on any single
concept, hand off to `praxis-learning`.

This skill is an **engine + a registry of flows**. The engine (below) runs
any flow. Each flow is a separate file under `flows/`. More flows get added
over time; the engine does not change.

## Dispatch — how a session starts

1. **Read progress.** Load `~/.praxis/onboarding-progress.json` (see format
   below). If absent, treat as a clean start.
2. **List flows** from the registry and let the user pick. If a flow is
   already in progress, offer to **resume it** at the saved step, or restart.
3. **Open the chosen flow file** (e.g. `flows/first-deployment.md`) and run
   it stop-by-stop with the engine loop. The flow file is the authoritative
   script for its stops; this engine governs *how* you run any stop.

## The engine loop — run every stop this way

For each stop in the flow:

1. **Teach.** A short concept (2–6 sentences) + a **mandatory ASCII diagram**
   showing the hierarchy/flow/relationship. Tutorial tone, not docs.
2. **Act.** Run the stop's command. Read-only stops run freely. **Mutating
   stops: see Safety below — confirm first, every time.**
3. **Check.** Ask: *"Got it? — yes / explain more / skip ahead."* Wait for the
   answer. On "explain more", re-explain with a different angle, then re-ask.
4. **Record.** Append the completed step index to the progress file under this
   flow's id (see format). This is what makes resume work.

Only advance after the check passes. Never batch stops silently.

## Safety — tiered confirmation

A blanket "trust you / don't ask me / go fast" authorizes *which approach to
take* and lets you skip nagging on cheap, reversible steps. It never
authorizes spending money or destroying things without an explicit yes.
Sort every mutation into one of two tiers:

**HARD GATE — explicit yes ALWAYS, even under "don't ask me".** Show the exact
command, name the consequence, wait for a separate, explicit yes. No
exceptions, no defaults-to-continue:
- the **sandbox confirmation** (Stop 0) — proof this control plane/cloud is
  safe to create and destroy in, before any mutation;
- anything **billable** — the release that provisions real cloud infra. State
  plainly it creates billable resources and roughly what, then get the yes;
- anything **destructive** — teardown / destroy applies.

**SOFT — free and reversible.** Catalog import, a module-spec tweak, project
and environment creation. These cost nothing and are reversible. *If* the user
opted into autonomy ("just do it / go fast"), you may **announce the exact
command and proceed** without a blocking yes. Otherwise, confirm normally.

Two rules that hold in both tiers:
- **`run_raptor_cli` is general-purpose.** Issue only the specific documented
  command the current stop calls for. Never improvise extra mutations.
- **Always offer teardown** at the end of any flow that deployed real
  resources — even if the user is in a hurry. The destroy itself is a HARD
  GATE.

### Red flags — you are rationalizing if you think…

| Thought | Reality |
|---------|---------|
| "They said don't ask, so I'll just run the release" | Billable = HARD GATE. Name the cost, get the yes. |
| "Teardown is optional since they're in a hurry" | Always offer it. Leaving infra running bills them silently. |
| "I'll skip the sandbox check to save a step" | That check is what stops you destroying something real. |
| "This destroy is fine, they trusted me" | Destructive = HARD GATE, always an explicit yes. |
| "Confirming the cheap steps too keeps it consistent" | Over-nagging kills an onboarding flow. SOFT steps may proceed when autonomy was granted. |

## Progress file format

`~/.praxis/onboarding-progress.json`:

```json
{
  "flows": {
    "first-deployment": { "completed_steps": [0,1,2], "updated_at": "2026-05-27T10:00:00Z" }
  }
}
```

Read it at dispatch; write it after each completed stop. On resume, the
highest completed step + 1 is where to continue. Create the file if missing.

## Flow registry

| id | title | file | what it does |
|----|-------|------|--------------|
| `first-deployment` | First deployment (sample, with teardown) | `flows/first-deployment.md` | Detect & link CP → ensure modules (import only if empty) → tweak a module → project → env → real release → verify → teardown |

**Adding a flow later:** drop a new file in `flows/`, add one row here. The
engine loop, safety rules, and progress format are reused unchanged.

## Handoffs

When a stop reaches a topic another skill owns, name it and defer:

- `praxis-build-facets-module` / `praxis-design-facets-module` — authoring or
  editing a module's contract (the "tweak a module" beat).
- `praxis-facets-blueprint` — projects, environments, releases, overrides.
- `praxis-learning` — a deeper conceptual chapter + quiz on any single topic.
- `praxis-release-debugging` / `troubleshoot` — when a release fails; read the
  real logs together (a failure is a teaching moment, not a dead end).

## Key principles

- **Journey, not lookup.** Pace the user; check understanding between stops.
- **Real over theory.** Every concept is paired with a real command against
  their own control plane.
- **Safe by default.** Confirm mutations per the tiers, name costs, always
  offer teardown.
- **Discover, don't invent.** Precede an unfamiliar `run_raptor_cli` verb with
  `<noun> --help` to get real flags rather than guessing.
