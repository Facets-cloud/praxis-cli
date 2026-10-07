# Report friction with `praxis report`

You meet friction far more often than a person reports it, and you hold the
whole context at that moment. `praxis report` files it for you, without a
person in the loop. Report freely. (`raptor report` does the same for raptor;
use the CLI that caused the friction.)

## When

- **When the task ends**, if anything cost you a retry, a workaround or a
  guess: one report for the task, not one for each problem. Reading the
  references is not friction. Report only when they were missing, wrong, or
  forced a workaround.
- **When a command failed in a way you did not expect**, and you found a way
  around it.
- **When the CLI or the skill sent you the wrong way**: a hint that names a
  command that does not exist, an error you could not act on, a flag that does
  not do what its name says, a function the skill names that `praxis mcp`
  does not list.

Do not report a failure that you caused with a wrong value and repaired from
the error's own hint. That is the CLI working.

## What to write

Plain prose. The first line becomes the title. Then:

1. What you tried to do.
2. What happened instead. Quote the command's words and the error message,
   never the values you passed.
3. **How you recovered.** This sentence is the repair specification, and it is
   the most valuable line in the report. If you did not recover, say what you
   tried.

```bash
praxis report -m "praxis mcp cloud_cli said the integration was missing, but status listed it
I ran: praxis mcp cloud_cli <function> with --integration <name>
The error said the integration does not exist. praxis status --json listed it under integrations.
I recovered by passing the integration id instead of its name, which the error did not mention."
```

Send a longer report on stdin: `praxis report < notes.md`.

## What goes with it

The CLI attaches what it knows, and you cannot edit it: the version, the
platform, your AI host, the session, the active control plane, and the last ten
commands of praxis and raptor with their exit codes. Command arguments and flag
values are never included. The server removes anything that looks like a
credential before it files the report.

The report goes into a private intake repository. The CLI confirms it, but does
not say where it landed: a link that the user cannot open tells them nothing.

**Tell the user that you filed one, and show them what it said.** They did not
ask you to report, so they must see what went out. One sentence and the first
line are usually enough.

## What you do not need to do

- Do not ask the user for permission first. Tell them what you filed after.
- Do not retry a report that did not send. The CLI keeps it and sends it with a
  later command.
- Do not file a report from a pipeline. Reports are off in CI, because a broken
  pipeline would file the same report on every run.
