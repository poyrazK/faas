/**
 * Crash snapshots from inside an app (ADR-733 SDK trigger).
 *
 * Call {@link captureCrashSnapshot} from an error handler, before the error
 * is handled, to capture the app's own instance while the failing request's
 * state is still in memory. The call blocks while Gregale pauses and
 * snapshots the instance, then the instance resumes and the call returns.
 * Open the capture later as a fork (`POST /v1/apps/{slug}/crash-snapshots/{id}/fork`).
 *
 * In a fork of that capture the same call returns again with `inFork: true`,
 * so the handler can continue in the debug copy (for example, log more).
 *
 * The app must opt in (`PUT /v1/apps/{slug}/crash-snapshots/settings`); refused requests
 * (no opt-in, a capture in flight, within the cooldown) return
 * `status: 'refused'` instead of throwing.
 */

/** Default in-guest metadata endpoint for the capture request. */
export const CRASH_SNAPSHOT_ENDPOINT = 'http://169.254.169.254/v1/crash-snapshots:capture';

export type CrashSnapshotStatus =
  | 'captured'
  | 'pending'
  | 'refused'
  | 'failed'
  | 'not_enabled'
  | 'unavailable'
  | 'invalid_request';

export interface CrashSnapshotOptions {
  /** Label stored with the capture (at most 256 bytes). */
  reason?: string;
  /** Path of the failing request, shown with the capture. */
  route?: string;
  /** How long to wait for the capture, in ms (default 15000, max 30000). */
  waitMs?: number;
  /** Override the endpoint (tests). */
  endpoint?: string;
  /** Override fetch (tests). */
  fetch?: typeof fetch;
}

export interface CrashSnapshotResult {
  status: CrashSnapshotStatus;
  captureId?: string;
  /** Failure code when status is 'failed' or 'unavailable'. */
  code?: string;
  /** True when this code is running in a fork restored from the capture. */
  inFork: boolean;
}

/**
 * Ask Gregale to capture this instance now. Never throws: outside Gregale,
 * or when the endpoint is unreachable, it resolves `status: 'unavailable'`.
 */
export async function captureCrashSnapshot(options: CrashSnapshotOptions = {}): Promise<CrashSnapshotResult> {
  const doFetch = options.fetch ?? fetch;
  const body: Record<string, unknown> = {};
  if (options.reason) body.reason = options.reason;
  if (options.route) body.route = options.route;
  if (options.waitMs !== undefined) body.wait_ms = Math.trunc(options.waitMs);
  const waitMs = Math.min(Math.max(options.waitMs ?? 15000, 0) || 15000, 30000);
  try {
    const response = await doFetch(options.endpoint ?? CRASH_SNAPSHOT_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(waitMs + 20000),
    });
    const parsed = (await response.json()) as { status?: string; capture_id?: string; code?: string; in_fork?: boolean };
    return {
      status: (parsed.status ?? 'unavailable') as CrashSnapshotStatus,
      captureId: parsed.capture_id || undefined,
      code: parsed.code || undefined,
      inFork: parsed.in_fork === true,
    };
  } catch {
    return { status: 'unavailable', inFork: false };
  }
}
