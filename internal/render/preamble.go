package render

// ExecutionPreamble is inserted after the YAML frontmatter when a skill or
// agent is installed onto a local AI host. Skill / agent authors write tool
// references assuming in-process MCP transport ("call run_k8s_cli", "the
// cloud_cli MCP server", etc.); the local AI host doesn't have those tools —
// only the `praxis` CLI as a subprocess. The preamble teaches Claude / Gemini /
// Codex the rewrite once, so the original body never has to change.
//
// Kept terse on purpose — the model is good at applying rules from a short
// rationale + one concrete example. Long preambles burn context in every
// conversation that loads the skill / agent.
const ExecutionPreamble = "" +
	"> **Execution context** — this was authored for in-process MCP\n" +
	"> in agent-factory. You're running it from a local AI host installed by\n" +
	"> `praxis login`, so MCP tools are NOT directly callable here.\n" +
	"> Whenever this references an MCP tool, shell out to `praxis`:\n" +
	">\n" +
	"> ```\n" +
	"> # Says:          run_k8s_cli(integration_name=\"prod\", command=\"get pods\")\n" +
	"> # You run:       praxis mcp k8s_cli run_k8s_cli \\\n" +
	">                  --arg integration_name=prod --arg command='get pods'\n" +
	"> ```\n" +
	">\n" +
	"> Rewrite rule: any `<mcp>.<fn>(args)` or bare `<fn>` reference becomes\n" +
	"> `praxis mcp <mcp> <fn> --arg k=v ...` (or `--body '<json>'` for nested\n" +
	"> args). The CLI authenticates as your Praxis user and runs the call\n" +
	"> server-side under your org's managed cloud / k8s credentials — your\n" +
	"> laptop never holds AWS / kube / terraform secrets.\n" +
	">\n" +
	"> If `praxis mcp <mcp> <fn>` returns 404, that tool isn't yet exposed\n" +
	"> by the gateway; fall back to whatever non-MCP path the body suggests.\n" +
	">\n" +
	"> **`raptor` is the exception — it is a LOCAL CLI, not a gateway tool.**\n" +
	"> Run `raptor …` commands directly in your shell; never route them\n" +
	"> through `praxis mcp` (there is no `raptor_cli` gateway tool). If\n" +
	"> `command -v raptor` finds nothing, ask the user to install it; if\n" +
	"> `raptor whoami` fails, ask the user to run `raptor login` first.\n" +
	">\n" +
	"> **Discovering what's available** — to see every MCP and function the\n" +
	"> gateway exposes, run `praxis mcp --json` (live fetch). A snapshot\n" +
	"> from your last `praxis login` lives at `~/.praxis/mcp-tools.json` —\n" +
	"> grep that file when you need the tool list without making a network call.\n"
