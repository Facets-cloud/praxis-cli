#!/usr/bin/env bash
# End-to-end check of `praxis login` from a genuinely clean machine.
#
# Hides your real credentials first, runs the whole flow, asserts every step,
# and puts them back at the end (including on Ctrl-C). Nothing is deleted: a
# credential that did not exist before the run is left in place, not removed.
#
#   ./scripts/e2e-login-check.sh https://facetsdemo.console.facets.cloud
#
# You will be asked to create a PAT in the browser and paste it back — that is
# the flow under test.

set -uo pipefail
CP="${1:?usage: e2e-login-check.sh <control-plane-url>}"
TS=$(date +%Y%m%d-%H%M%S)
PASS=0; FAIL=0

ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

# --- hide, and guarantee we put them back -------------------------------
hide() {
  for f in "$HOME/.facets/credentials" "$HOME/.praxis/credentials"; do
    [ -f "$f" ] && mv "$f" "$f.e2e-$TS" && echo "  hidden: $f"
  done
  return 0
}
restore() {
  step "Restoring your real credentials"
  for f in "$HOME/.facets/credentials" "$HOME/.praxis/credentials"; do
    if [ -f "$f.e2e-$TS" ]; then
      mv -f "$f.e2e-$TS" "$f" && echo "  restored: $f"
    elif [ -f "$f" ]; then
      # Nothing to restore: you had no credential here before the run. The one
      # the test just created is valid, so it is kept rather than deleted.
      echo "  kept (none existed before): $f"
    fi
  done
  return 0
}
trap restore EXIT INT TERM
# Ctrl-Z does NOT fire EXIT. Without this the run suspends with both credential
# files still hidden, which is how a stalled login strands them.
trap 'restore; trap - TSTP; kill -TSTP $$' TSTP

step "1. Hiding real credentials"
hide

step "2. Confirming a clean machine"
praxis status --json 2>/dev/null | jq -e '.logged_in == false' >/dev/null \
  && ok "praxis reports logged out" || bad "praxis still thinks it is logged in"
raptor whoami >/dev/null 2>&1 \
  && bad "raptor still authenticated" || ok "raptor reports no credentials"
[ -f "$HOME/.facets/credentials" ] \
  && bad "~/.facets/credentials still present" || ok "~/.facets/credentials absent"

step "3. Running the real login (create a PAT and paste it back)"
echo "  If this stalls, press Ctrl-C (not Ctrl-Z) — credentials restore on Ctrl-C."
praxis login --url "$CP"
LOGIN_RC=$?
[ $LOGIN_RC -eq 0 ] && ok "login exited 0" || bad "login exited $LOGIN_RC"

step "4. Did praxis save its own credentials?"
[ -f "$HOME/.praxis/credentials" ] \
  && ok "~/.praxis/credentials written" || bad "~/.praxis/credentials MISSING"

step "5. Did praxis seed raptor? (the fix)"
if [ -f "$HOME/.facets/credentials" ]; then
  ok "~/.facets/credentials written"
  [ "$(stat -f '%Sp' "$HOME/.facets/credentials")" = "-rw-------" ] \
    && ok "mode is 0600" || bad "mode is not 0600"
  # Assert through raptor's own reader, not this file's format, so the check
  # cannot go stale when the writer changes.
  raptor whoami 2>/dev/null | grep -q "$CP" \
    && ok "raptor reads it and reports $CP" || bad "raptor does not read $CP from it"
else
  bad "~/.facets/credentials MISSING — the seed did not happen"
fi

step "6. Does raptor actually work with it?"
raptor whoami >/dev/null 2>&1 \
  && ok "raptor whoami succeeds" || bad "raptor whoami still fails"
raptor get projects -o json 2>/dev/null | jq -e 'length > 0' >/dev/null \
  && ok "raptor fetched real projects from the control plane" \
  || bad "raptor could not fetch projects"

step "7. Does praxis consider setup complete?"
praxis status --json 2>/dev/null | jq -e '.setup_complete == true' >/dev/null \
  && ok "setup_complete = true" || bad "setup_complete is still false"
praxis status --json 2>/dev/null | jq -e '.raptor.matches_praxis_url == true' >/dev/null \
  && ok "raptor and praxis agree on the control plane" || bad "control planes disagree"

step "8. Did org skills install? (needs the agent-factory fix DEPLOYED)"
SKILLS=$(praxis status --json 2>/dev/null | jq '.skills_installed | length')
echo "  skills installed: $SKILLS"
echo "  If login printed 'catalog fetch failed: HTTP 403 .../skills/bundle',"
echo "  this deployment does not yet have the cli_skills.py fix. Everything"
echo "  above is unaffected by that."

step "Result"
printf '  passed: %d   failed: %d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
