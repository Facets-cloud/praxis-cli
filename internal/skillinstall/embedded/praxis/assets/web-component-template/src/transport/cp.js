/**
 * Tunnel transport. Every browser-side call to cluster tooling goes through here.
 *
 * Three rules are baked in because each one is a defect someone already paid for:
 *   1. dependent environments route through `baseClusterId`
 *   2. a dead tunnel HANGS rather than failing, and shares the browser's
 *      per-origin connection pool — so a missing timeout starves the whole app
 *   3. `/cc-ui/v1/.../k8s-explorer/*` 500s when called in parallel
 */

const TUNNEL_TIMEOUT_MS = 15_000;

/** clusterId -> Promise<tunnelClusterId>. Stable for the session. */
const tunnelClusterIds = new Map();

/**
 * Dependent (sub-)environments do not run their own tooling — traffic routes
 * through the base cluster. Skip this and every widget is blank on every
 * sub-environment, with nothing in the console to explain why.
 */
export function resolveTunnelClusterId(clusterId, origin = window.location.origin) {
  const cached = tunnelClusterIds.get(clusterId);
  if (cached) return cached;

  const promise = fetch(`${origin}/cc-ui/v1/clusters/${clusterId}`, { credentials: 'include' })
    .then((r) => (r.ok ? r.json() : null))
    .then((c) => c?.baseClusterId || clusterId)
    .catch(() => {
      // Don't poison the cache: fall back for this call, allow a later retry.
      tunnelClusterIds.delete(clusterId);
      return clusterId;
    });

  tunnelClusterIds.set(clusterId, promise);
  return promise;
}

/** fetch with a hard timeout. Mandatory for tunnel URLs, not defensive. */
export async function fetchWithTimeout(url, { signal, timeoutMs = TUNNEL_TIMEOUT_MS, ...rest } = {}) {
  const timeout = AbortSignal.timeout(timeoutMs);
  const combined = signal ? AbortSignal.any([signal, timeout]) : timeout;
  return fetch(url, { ...rest, credentials: 'include', signal: combined });
}

/** Base URL for tunnelled Grafana. Everything after `/grafana/` is forwarded verbatim. */
export async function grafanaBase(clusterId, origin = window.location.origin) {
  const tunnelId = await resolveTunnelClusterId(clusterId, origin);
  return `${origin}/tunnel/${tunnelId}/grafana`;
}

/**
 * Single-slot queue for the k8s explorer.
 *
 * Not rate limiting you can retry past — parallelism itself is the fault
 * (3 of 4 fail concurrently; 12/12 succeed serially). Other `/cc-ui/v1` reads do
 * NOT need this — do not route them through the queue or the page crawls.
 */
let explorerChain = Promise.resolve();

export function explorerFetch(clusterId, kind, { signal, origin = window.location.origin } = {}) {
  const run = async () => {
    const url = `${origin}/cc-ui/v1/clusters/${clusterId}/k8s-explorer/${kind}`;
    // No query params: `labels` is declared required in the spec but is a
    // catch-all @RequestParam Map — sending nothing returns 200.
    const res = await fetchWithTimeout(url, { signal });
    if (!res.ok) throw new Error(`k8s-explorer/${kind} returned ${res.status}`);
    return res.json();
  };

  // Chain regardless of the previous call's outcome, so one failure does not
  // wedge the queue for the rest of the session.
  const result = explorerChain.then(run, run);
  explorerChain = result.then(
    () => undefined,
    () => undefined
  );
  return result;
}
