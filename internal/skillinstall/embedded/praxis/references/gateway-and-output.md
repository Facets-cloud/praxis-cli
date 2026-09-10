# Gateway and output

`praxis mcp --json` lists the live function manifest; it is the authority for
namespaces, functions, argument names and available operations. No arguments
lists; exactly two positional arguments invoke. A namespace alone is not help.
`praxis mcp --help` describes local transport flags, not every deployed function.

```bash
praxis mcp cloud_cli run_cloud_cli --json \
  --arg integration_name=ACCOUNT \
  --arg command='ec2 describe-instances --output json'
```

The provider prefix is omitted **inside** the cloud command. This command runs
on the server under the selected integration, not in the caller's checkout.

## Request construction

- Repeat `--arg key=value` for different top-level keys. JSON-looking values
  become booleans/numbers/arrays/objects; others remain strings. Repeated **same**
  keys overwrite, not append. Use a JSON array for list-valued arguments.
- `--body` **replaces all `--arg` values**, not merges with them. Include every
  argument, including integration, in one JSON object. Do not send null/arrays.
- `--body -` reads stdin; use it for nested data and long document bodies.
  For example, `jq -n --rawfile content report.md ... | praxis mcp ... --body -`.
- Provider command strings do not inherit your shell variables, pipes or files.
  Use supported `jq_expression`/`grep_pattern` parameters for remote filtering.
  A server `output_file` is a server-side file, not a laptop download.

## Response contract

Explicit `--json` avoids TTY-dependent formatting. In JSON mode:

| Response | Default stdout |
|---|---|
| Successful single text block containing valid JSON | That parsed JSON, including scalar/null |
| Plain text, multiple blocks, non-text blocks | Raw MCP envelope |
| Tool-level `isError: true` | Raw envelope and nonzero exit (even HTTP 200) |
| `--envelope` | Raw response envelope, no successful JSON unwrapping |

Capture exit status before transforming output. Inspect the complete tool result
on failure. HTTP/CLI failures can use a different error shape; missing fields
must not silently become success or empty arrays. Check domain fields too: an
unwrapped `ok:false` is not success just because HTTP was 200.

For a known JSON-producing function, parse the default response directly. Do not
blindly use `.content[0].text | fromjson`, which belongs to old CLI examples.
For generic tooling choose `--json --envelope`, validate envelope shape, preserve
all content blocks, and parse a text block only when its content is valid JSON.
Never discard multipart/error content to make a parser appear to work. Decode
JSON once rather than emitting raw control characters into a second JSON parser.

## Failure and completion

Unknown function: re-check the deployed manifest. Auth/permission errors: report
the boundary. Timeout/network loss after a write: inspect operation identity and
server state before retrying; client timeout does not cancel server work.
Async tools may return a job/run ID immediately. Poll that exact ID with bounded
checks using the host's monitoring facilities; inspect its final result, not the
most recent unrelated run. Report any pagination limit, sample size, truncated
output or unvisited integration when making an absence/completeness claim.
