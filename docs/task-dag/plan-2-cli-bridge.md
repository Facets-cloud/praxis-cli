# Task-DAG CLI Bridge (praxis-cli) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The laptop half of the judge-gated task DAG: the `praxis dag` verb tree (machine-first, always `--json`), the bridge that turns claimed nodes into Claude sessions (flow-first, bare `claude -p` fallback), the local judge runner with the inner converge loop, and the submit-envelope assembly (redaction + transcript upload).

**Architecture:** Thick CLI, thin skill (spec D12): all orchestration intelligence lives here, implemented once in Go; skills (served by agent-factory) only tell coding agents which verbs to call. Verbs talk to the agent-factory gateway (`POST {baseURL}/ai-api/v1/mcp/task_dag/<fn>`, Bearer auth via `credentials.ResolveActive`) using the `internal/memory/client.go` `doJSON[T]` idiom. The bridge is deterministic Go — no model ever assembles another model's prompt (judge blindness, spec §5). Everything is driven by Claude, not humans (spec D6): every verb is non-interactive and idempotent.

**Tech Stack:** Go 1.24, spf13/cobra (nested tree per `cmd/memory.go` precedent), `os/exec` for spawning `flow` / `claude` / `agy`, existing `internal/harness` binary detection, `internal/credentials`, httptest for gateway fakes.

**Spec:** agent-factory PR "docs(task-dag)" → `docs/task-dag/design-spec.md`. Companion plans there: plan-1 (server substrate — its gateway fns and contract schemas are this plan's upstream), plan-3 (skills).

## Global Constraints

- House rules (CLAUDE.md): every `internal/<pkg>` has `<pkg>_test.go`, ≥75% coverage, no stub commands — a verb ships working or not at all.
- Every verb supports `--json` and is safe under agent retries: `start` sends an idempotency key; `submit` carries the attempt token and treats 409 as a terminal "someone else finished this attempt" (never retried).
- The worker never spawns the judge; the judge prompt is assembled only by `internal/dag/judge` from the server's judge-packet (which never contains transcripts).
- Transcript upload happens only after the redaction pass; redaction runs on the full transcript before any bytes leave the laptop (spec §13).
- Exec seams are package-level `var`s (repo idiom: `callMCP`, `Fetch`) so tests can stub subprocess and HTTP calls without running real binaries.

## File Structure

```
cmd/dag.go                        # Task 3 — cobra verb tree (thin shims)
internal/dag/contracts.go         # Task 1 — vendored schema types + CI drift check
internal/dag/client.go            # Task 2 — gateway client (doJSON idiom)
internal/dag/packet.go            # Task 4 — work packet → flow brief / prompt rendering
internal/dag/spawn.go             # Task 5 — session spawning: flow-first, claude -p fallback
internal/dag/judge.go             # Task 6 — judge prompt assembly + verdict parsing
internal/dag/converge.go          # Task 7 — worker↔judge inner loop
internal/dag/redact.go            # Task 8 — transcript redaction pass
internal/dag/envelope.go          # Task 8 — submit envelope assembly
internal/dag/tick.go              # Task 9 — `dag tick`: one runner-owner tick
internal/dag/adopt.go             # Task 9 — owner creation via flow
internal/dag/*_test.go            # every task
```

---

### Task 1: Vendored contract types + drift check

**Files:**
- Create: `internal/dag/contracts.go`, `internal/dag/contracts/` (vendored copies of the three agent-factory `contracts/task_dag/*.schema.json`)
- Create: `internal/dag/contracts_test.go`
- Modify: CI workflow (add a job step `diff` between vendored schemas and the agent-factory copies fetched by URL, warn-only)

**Interfaces:**
- Produces Go types used by every later task, mirroring the schemas exactly:

```go
type WorkPacket struct {
    RunID, NodeID, AttemptToken string
    Brief                       string
    InputPayloads               map[string]json.RawMessage
    OutputSchema                json.RawMessage
    SuccessCriteria             []string
    Judge                       string // agent|human|agent_then_human
    JudgeMode                   string // evidence|verify
    MaxIterations               int
    LeaseExpiresAt              time.Time
}
type Verdict struct {
    Pass       bool        `json:"pass"`
    Checks     []Check     `json:"checks"`
    Missing    []string    `json:"missing"`
    Confidence float64     `json:"confidence"`
}
type Check struct{ Criterion string; Met bool; Note string }
type SubmitEnvelope struct {
    AttemptToken   string
    Result         json.RawMessage
    Summary        Summary        // Did string; Decisions, Deviations []string
    Artifacts      []ArtifactRef  // Type, URI, SHA256 string; Size int64
    Verdict        Verdict
    IterationTrail []Iteration    // I int; Verdict Verdict; Feedback string
    JudgePrompt    string
    PromptDigest   string
    Provenance     Provenance
    Transcript     string         // full, post-redaction
}
```

- [ ] Failing test: `Verdict` round-trips against `contracts/verdict.schema.json` required fields (unmarshal a valid fixture, marshal, validate keys present). → implement → pass → commit.

---

### Task 2: Gateway client

**Files:**
- Create: `internal/dag/client.go`, `internal/dag/client_test.go`

**Interfaces:**
- Consumes: `credentials.ResolveActive("")` for `{URL, Token}` (same as `cmd/mcp.go`).
- Produces: `Client` with one method per gateway fn, all `POST {base}/ai-api/v1/mcp/task_dag/<fn>`:

```go
func NewClient() (*Client, error) // resolves active profile
func (c *Client) StartRun(templateID string, params map[string]string, idemKey string) (StartResult, error)
func (c *Client) Claim(runID, nodeID string, capabilities []string, manual bool) (*WorkPacket, error) // nil = nothing claimable
func (c *Client) Heartbeat(runID, nodeID, token string) error            // ErrStaleAttempt on 409
func (c *Client) JudgePacket(runID, nodeID string) (JudgePacket, error)
func (c *Client) Submit(runID, nodeID string, env SubmitEnvelope) (SubmitResult, error) // ErrStaleAttempt on 409
func (c *Client) Status(runID string) (RunStatus, error)
func (c *Client) Approve(runID, nodeID string, approve bool, feedback string) error
func (c *Client) Rollback(runID, target string) error
func (c *Client) Templates() ([]TemplateSummary, error)
func (c *Client) AdoptInfo(runID string) (AdoptInfo, error)
```

Copy the `doJSON[T any]` helper shape from `internal/memory/client.go` (bounded timeout, `http.NewRequestWithContext`); expose the HTTP door as `var httpDo = http.DefaultClient.Do` for tests.

- [ ] Failing tests with `httptest.Server`: URL/auth-header shape; 409 → `ErrStaleAttempt`; claim-nothing → `nil, nil`. → implement → pass → commit.

---

### Task 3: `cmd/dag.go` verb tree

**Files:**
- Create: `cmd/dag.go`, `cmd/dag_test.go`

**Interfaces:**
- Consumes: Task 2 `Client`, later Tasks 5–9 functions (wired as they land; verbs shipped only with their working internals — no stubs, so this task lands LAST in commit order but is written now as the spine and kept in a branch-local commit until its dependencies exist. Alternative if strict no-stub CI blocks: land `templates|start|status|approve|rollback` (pure client passthroughs) here, and add `claim|tick|adopt` in Tasks 5/9).
- Produces the tree (modeled on `cmd/memory.go`):

```
praxis dag templates                       --json
praxis dag start <template> --param k=v … --idem <key> --json
praxis dag adopt <run-id>                  --json
praxis dag status <run-id>                 --json
praxis dag approve <run-id> <node-id>      [--feedback s] --json
praxis dag reject  <run-id> <node-id>      [--feedback s] --json
praxis dag rollback <run-id> --to <node>   --json
praxis dag claim <run-id> [--node <id>] [--manual] --json   # person taking a node
praxis dag tick <run-id>                   --json           # one runner-owner tick
```

Every RunE: parse flags → client/bridge call → `json.NewEncoder(os.Stdout).Encode(...)`. No prompts, no confirmation reads — machine-first (spec D6). `start` auto-generates the idempotency key from `(template, sorted params, user)` when `--idem` absent.

- [ ] Failing tests: command registration (`rootCmd` has `dag`, `dag` has all children); `start` flag parsing round-trip with stubbed client. → implement passthrough verbs → pass → commit.

---

### Task 4: Work packet → flow brief rendering

**Files:**
- Create: `internal/dag/packet.go`, `internal/dag/packet_test.go`

**Interfaces:**
- Produces:

```go
// RenderBrief produces the node session's entire world (spec §9: node is DAG-blind).
func RenderBrief(p WorkPacket) string
// RenderResultInstruction tells the worker where/how to emit its result.
func ResultPath(workdir string) string // <workdir>/dag-result.json
```

`RenderBrief` emits flow-brief markdown: `# <title>` / `## What` (brief) / `## Inputs` (each input payload pretty-printed verbatim) / `## Done when` (success criteria bullets + "write `dag-result.json` at the workspace root conforming to the schema below, plus a `summary` block") / `## Output schema` (fenced JSON). It must contain **zero** DAG vocabulary: no run/node/claim/judge words.

- [ ] Failing test: golden-file comparison; assert `!strings.Contains(brief, "node")` etc. → implement → pass → commit.

---

### Task 5: Session spawning — flow-first, bare fallback

**Files:**
- Create: `internal/dag/spawn.go`, `internal/dag/spawn_test.go`

**Interfaces:**
- Consumes: Task 4 rendering; `internal/harness` binary detection.
- Produces:

```go
type Session struct {
    FlowSlug string        // "" when bare mode
    PID      int
    Workdir  string
}
type Spawner interface {
    Start(p WorkPacket, workdir string) (*Session, error)
    StatusOf(s *Session) SessionStatus // Running | Completed | Dead
    Continue(s *Session, instruction string) error // converge-loop feedback
    TranscriptOf(s *Session) (string, error)
    Close(s *Session) error // flow done | reap
}
func NewSpawner() Spawner // flow on PATH → flowSpawner, else bareSpawner
```

`flowSpawner`: `flow add task "dag: <title>" --slug dag-<run8>-<node> --tag dag:<run> --tag node:<id> --work-dir <wd> --mkdir` → write brief → `flow do --auto <slug>`; `StatusOf` parses `flow show task <slug>` `auto_run:` line; `Continue` = `flow do --auto <slug> --with <instruction>`; `TranscriptOf` = `flow transcript <slug>`; `Close` = no-op (auto runs self-`flow done`). `bareSpawner`: `claude -p < brief` detached with PID tracking, transcript from harness jsonl path. All exec via `var execCommand = exec.Command` seam.

- [ ] Failing tests with a fake `execCommand` (the repo's stub-binary trick or a recording fake): correct flow argv sequences; auto_run parsing (`running (pid 4242)` / `completed` / `dead`); bare fallback selected when `LookPath("flow")` fails. → implement → pass → commit.

---

### Task 6: Judge runner

**Files:**
- Create: `internal/dag/judge.go`, `internal/dag/judge_test.go`

**Interfaces:**
- Consumes: Task 1 types, Task 2 `JudgePacket`.
- Produces:

```go
// AssembleJudgePrompt is the ONLY place a judge prompt is built (deterministic).
func AssembleJudgePrompt(jp JudgePacket, result json.RawMessage, artifacts []ArtifactRef) (prompt string, digest string)
// RunJudge spawns a fresh judge subagent and parses its verdict.
func RunJudge(prompt string, mode string) (Verdict, error) // mode: evidence|verify
var judgeCLI = detectJudgeCLI // claude|agy via harness LookPath, override PRAXIS_DAG_JUDGE_CLI
```

Prompt layout (golden-tested): criteria numbered; evidence = result JSON + artifact list (type/uri/sha); the closing instruction `Respond with ONLY JSON: {"pass":bool,"checks":[{"criterion","met","note"}],"missing":[],"confidence":0-1}`. `evidence` mode runs `claude -p --output-format json` with tools disabled; `verify` mode allows read-only tools (`--allowedTools "Read,Bash(git *),Bash(gh *),Bash(kubectl get *),Bash(curl *)"`). Digest = sha256(prompt). Never accepts transcript input — signature makes it impossible.

- [ ] Failing tests: golden prompt; verdict JSON parsing incl. fenced-JSON tolerance; malformed verdict → one respawn then error. → implement → pass → commit.

---

### Task 7: Inner converge loop

**Files:**
- Create: `internal/dag/converge.go`, `internal/dag/converge_test.go`

**Interfaces:**
- Consumes: Tasks 5, 6 exactly as produced.
- Produces:

```go
type ConvergeOutcome struct {
    Final     Verdict
    Trail     []Iteration
    Result    json.RawMessage
    Converged bool // false = max_iterations exhausted
}
func Converge(sp Spawner, s *Session, p WorkPacket, jp JudgePacket) (ConvergeOutcome, error)
```

Loop: read `dag-result.json` from workdir → validate against `p.OutputSchema` (schema-invalid counts as an iteration with a synthetic verdict `missing: ["result.json does not match output schema: …"]`) → `AssembleJudgePrompt` → `RunJudge` → pass? done : `sp.Continue(s, "judge feedback — address and update dag-result.json: <missing…>")`, wait for `Completed`, repeat. Hard cap `p.MaxIterations`.

- [ ] Failing tests with fake Spawner + fake judge: pass-first-try (trail len 1); fail-then-pass (Continue called with missing items verbatim); exhaustion (`Converged=false`, trail len = max). → implement → pass → commit.

---

### Task 8: Redaction + envelope assembly

**Files:**
- Create: `internal/dag/redact.go`, `internal/dag/envelope.go`, tests for both

**Interfaces:**

```go
func Redact(transcript string) (clean string, manifest RedactionManifest)
// patterns: AWS keys (AKIA…/secret pairs), bearer/authorization headers,
// PEM blocks, env-styled secrets (\b\w*(TOKEN|SECRET|PASSWORD|API_KEY)\w*=\S+),
// gh/ssh URLs with embedded credentials. Replacement: [REDACTED:<kind>].
func BuildEnvelope(o ConvergeOutcome, s *Session, sp Spawner, p WorkPacket,
                   prompt, digest string, prov Provenance) (SubmitEnvelope, error)
```

`BuildEnvelope` reads worker summary from `dag-result.json`'s `summary` key, collects artifact refs (result-declared paths → sha256 them), pulls transcript via `sp.TranscriptOf`, redacts, attaches manifest into provenance.

- [ ] Failing tests: each redaction pattern (table-driven); envelope required-fields completeness against vendored schema; artifact sha256 correctness. → implement → pass → commit.

---

### Task 9: `dag tick` + `dag adopt` + `dag claim` (manual)

**Files:**
- Create: `internal/dag/tick.go`, `internal/dag/adopt.go`, tests
- Modify: `cmd/dag.go` (wire the three verbs)

**Interfaces:**
- Consumes: everything above.
- Produces:

```go
// Tick performs ONE runner-owner tick; the praxis-dag-runner skill calls this.
func Tick(c *Client, sp Spawner, runID string, parallelism int) (TickReport, error)
type TickReport struct {
    Claimed    []string // node_ids newly claimed+dispatched
    Submitted  []struct{ NodeID string; State string }
    InFlight   []string // heartbeated
    NeedsHuman []string
    ManualReady []string // ready manual nodes (nudge, don't claim)
    RunStatus  string
}
// Adopt ensures the runner-owner exists (flow owner create if missing).
func Adopt(c *Client, runID string) (AdoptReport, error)
// ManualClaim claims a specific node for THIS session's human collaborator.
func ManualClaim(c *Client, sp Spawner, runID, nodeID string) (*WorkPacket, error)
```

`Tick` per invocation: `Claim(runID, "", caps, false)` in a loop up to free capacity → `sp.Start` each; for known in-flight sessions (state file `~/.praxis/dag/<run>.json` tracks session↔node): `StatusOf` — `Running`→`Heartbeat`, `Completed`→`Converge`+`BuildEnvelope`+`Submit`, `Dead`→drop (lease will expire server-side). `Adopt`: `AdoptInfo` → if no flow on PATH, print report with `owner: none (bare mode — schedule 'praxis dag tick' externally)`; else `flow add owner "dag-runner:<run8>" --every 30m` + write charter referencing the praxis-dag-runner skill.

- [ ] Failing tests with fakes: full tick happy path; dead session not resubmitted; manual node listed not claimed; state file round-trip. → implement → wire cmd verbs → pass → commit.

---

### Integration checkpoint (cross-repo smoke)

With substrate Tasks 1–7 deployed locally (agent-factory dev server): seed a 2-node template (`echo-a` → `echo-b`, trivial criteria), then:

```
praxis dag start smoke --param x=1 --json      # → run_id
praxis dag tick <run> --json                    # claims node a, spawns, converges, submits
praxis dag tick <run> --json                    # node b (readied by a's pass)
praxis dag status <run> --json                  # → status: completed
```

Assert: exactly one submit per node; verdict trail non-empty; transcript artifact present server-side; second `start` with same params+idem key returns the same run.

## Self-review notes

- Spec coverage: D5–D9, D11–D12 all land here; §4a manual claims (Task 9 `ManualClaim` + `--manual`); §5 converge loop (Task 7); §6 envelope (Task 8); §8 owner (Task 9 `Adopt`); §13 redaction (Task 8). Conductor UX (`dag conduct`) is v2 (spec §12) — deliberately absent.
- Judge blindness is structural at three layers: `JudgePacket` has no transcript field (server filters), `AssembleJudgePrompt` signature can't take one, and the worker never invokes the judge.
- No stub commands: passthrough verbs land in Task 3; bridge verbs only in Task 9 when their internals are complete.

---

## SUPERSEDED (16 Jul 2026, spec D14)

Substrate-only decision: the entire command surface is `praxis mcp task_dag <fn>`
(already shipped in cmd/mcp.go). No bridge/spawner/judge/converge/redact Go code;
praxis-cli ships nothing for task-DAG v1. Execution + judging choreography move to
skills (agent-factory plan-3); redaction + edge validation move server-side (plan-1).
This plan is retained for the record. See agent-factory PR #1531.
