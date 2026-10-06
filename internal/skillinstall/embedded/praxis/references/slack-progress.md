# Slack progress

Use one in-place tracker for an agreed long-running task, not a new message per
step. A one-shot announcement does not need a tracker. Confirm destination and
initial body before sending; continued edits must stay within the agreed tracker
scope. Posting is externally visible, not an automatic side effect of working.

Discover `integrations.list_chat_integrations` and deployed `slack.post_message`/
`update_message`. Gateway sends as the selected org bot; no local token/keychain
is needed. Choose integration ID when names collide. A single active integration
may be defaulted; zero/multiple requires a choice. Membership is Slack's access
boundary: `not_in_channel` needs a human invitation, not an automatic attempt to
add the bot. Another user's/bot's message cannot be edited by this bot.

## Tracker shape

Use a title and scope line, a fenced checklist with Unicode `✅ 🔄 ⬜`, a bold
`:rocket: Next:` line and refreshed timestamp. Shortcodes inside code fences
remain literal. For customer channels use the requested `- sent by Praxis` footer.
For this established customer format the timestamp is IST:
`TZ=Asia/Kolkata date '+%d %b %Y, %H:%M IST'`. Honor a different explicitly selected
channel convention; don't silently change sender attribution.

## Send, retain identity, update

Prepare reviewed text in a local nonsensitive file, then construct one complete
body (the integration belongs in it because `--body` overrides `--arg`):

```bash
jq -n --rawfile text tracker.md --arg channel CHANNEL_ID \
  --arg integration INTEGRATION_ID \
  '{channel:$channel,integration_id:$integration,text:$text,unfurl_links:false}' \
  | praxis mcp slack post_message --body - --json
```

Check CLI exit and payload `ok`, then retain **channel + integration + returned
`ts`**. Default JSON output is unwrapped for successful JSON responses; don't
use the obsolete `.content[0].text` parser. On an ambiguous send timeout,
reconcile message history/identity before retrying to avoid duplicates.

```bash
jq -n --rawfile text tracker.md --arg channel CHANNEL_ID \
  --arg integration INTEGRATION_ID --arg ts RETURNED_TS \
  '{channel:$channel,integration_id:$integration,message_ts:$ts,text:$text}' \
  | praxis mcp slack update_message --body - --json
```

Always update the same message and verify the API result. Refresh the timestamp
and next step, including blockers honestly. Retain IDs across session handoff,
not credentials. Message text is audit-logged: no secrets, raw credentials,
sensitive logs or misleading “complete” from an accepted-but-running job.
