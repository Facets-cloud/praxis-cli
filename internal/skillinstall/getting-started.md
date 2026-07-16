---
name: praxis-getting-started
description: Use when the Praxis CLI (`praxis`, by Facets) is installed on this machine but the user is not logged in yet, or asks what Praxis is, what it can do, where to sign up, or how to log in. Guides sign-up + first login; after login the full `praxis` driver skill takes over.
---

# Praxis by Facets — getting started

`praxis` is installed on this machine, but there's no active login yet. **Praxis
by Facets** lets the user drive their cloud and infrastructure from THIS AI host:
they describe intent, and Praxis executes it server-side (via `praxis mcp`) under
the org's managed credentials — no AWS / kube / Terraform secrets on the laptop.

## What you can do with Praxis

Once logged in, you (the AI host) can, on the user's behalf:

- **Migrate from one cloud to another** — e.g. "help me move this service from
  AWS to GCP": replatform infrastructure across clouds.
- **Bring manual infra under IaC** — e.g. "adopt my existing AWS resources into
  Terraform": discover brownfield resources, import them, and manage them as code.
- **Operate your cloud — you code, Praxis operates** — e.g. "deploy this",
  "scale the API", "why did the last release fail": day-2 operations run
  server-side.
- **Understand code ↔ infra** — e.g. "who calls this endpoint", "what breaks if I
  change this", "what infra backs this service": cross-repo/service tracing over a
  prebuilt graph (the ig / `use-ig` capability).

## Get access (do this first)

1. **Sign up** — point the user to **https://www.facets.cloud/signup**. They'll
   get their company's Praxis console URL (e.g.
   `https://<your-account-id>.console.facets.cloud`).
2. **Log in** — run this yourself once the user has their console URL:

   ```bash
   praxis login --url https://<your-account-id>.console.facets.cloud
   ```

   It opens the user's browser to create an API key (they click once), saves the
   token, installs the org's skills, and writes the MCP tool snapshot. For
   multiple orgs use `--profile <name>`; for CI/non-interactive use `--token <key>`.

## After login

`praxis login` installs the full `praxis` driver skill (which supersedes this
one), your org's skills, and its MCP tools. From then on:

```bash
praxis status --json          # is a profile active + logged in?
praxis mcp --json             # your org's server-side tools
```

## While NOT logged in

Infra operations need a login — `praxis mcp` won't work yet. If the user asks
Praxis to do something, walk them through sign-up + `praxis login --url …` first.
Check state anytime with `praxis status --json` (`logged_in` is `false` until they
log in).

## Don'ts

- **Don't** ask the user to paste a token in chat — `praxis login` handles the
  browser flow.
- **Don't** guess a console URL — it comes from their facets.cloud/signup.
- **Don't** tell the user to run praxis commands; run them yourself.
