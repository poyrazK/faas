// Internal bounded RPCs. Never retain remote bodies, request objects or raw errors:
// generated ApiError.request includes the secret acknowledgment capability.
import { EventsService } from './generated/services/EventsService.js';
import { OpenAPI } from './generated/core/OpenAPI.js';
import { request } from './generated/core/request.js';

export interface AcknowledgmentWorkerSettings {
  readonly callbackAttempts: number;
  readonly callbackTimeoutMs: number;
  readonly retryBackoffMs: number;
  readonly renewalIntervalMs: number;
}
export interface AcknowledgmentWorkerScope {
  readonly app: string;
  readonly invocationId: string;
  readonly token: string;
}
export type AcknowledgmentAction = 'get' | 'renew' | 'complete' | 'retry' | 'dead-letter';

export class AcknowledgmentCallbackFailure extends Error {
  constructor(readonly status?: number, readonly retryDelayMs?: number, readonly leaseLost = false) {
    super('event acknowledgment callback unconfirmed');
    this.name = 'AcknowledgmentCallbackFailure';
  }
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
function failure(error: unknown, settings: AcknowledgmentWorkerSettings): AcknowledgmentCallbackFailure {
  const status = record(error) && typeof error.status === 'number' && Number.isInteger(error.status)
    && error.status >= 100 && error.status <= 599 ? error.status : undefined;
  if (status !== undefined && status !== 408 && status !== 425 && status !== 429 && (status < 500 || status > 599)) {
    return new AcknowledgmentCallbackFailure(status);
  }
  let delay = settings.retryBackoffMs;
  const problem = record(error) ? (record(error.problem) ? error.problem : record(error.body) ? error.body : undefined) : undefined;
  const seconds = problem?.retry_after_seconds;
  if (seconds !== undefined && seconds !== null) {
    if (typeof seconds !== 'number' || !Number.isSafeInteger(seconds) || seconds < 0) return new AcknowledgmentCallbackFailure(status);
    delay = Math.max(delay, seconds * 1000);
  } else {
    const raw = record(error) && error.retryAfter !== undefined ? error.retryAfter : problem?.retry_after;
    if (raw !== undefined && raw !== null) {
      if (typeof raw !== 'string' || !raw.trim()) return new AcknowledgmentCallbackFailure(status);
      const hint = raw.trim();
      if (/^[0-9]+$/.test(hint)) {
        const value = Number(hint);
        if (!Number.isSafeInteger(value)) return new AcknowledgmentCallbackFailure(status);
        delay = Math.max(delay, value * 1000);
      } else {
        // IMF-fixdate is the HTTP sender format. Reject ambiguous date strings.
        const at = Date.parse(hint);
        if (!/^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), \d{2} (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) \d{4} \d{2}:\d{2}:\d{2} GMT$/.test(hint)
          || !Number.isFinite(at) || new Date(at).toUTCString() !== hint) return new AcknowledgmentCallbackFailure(status);
        delay = Math.max(delay, at - Date.now());
      }
    }
  }
  return new AcknowledgmentCallbackFailure(status,
    Number.isFinite(delay) && delay <= settings.renewalIntervalMs ? delay : undefined);
}

export async function acknowledgmentCall(scope: AcknowledgmentWorkerScope, settings: AcknowledgmentWorkerSettings,
  action: AcknowledgmentAction, signal?: AbortSignal, value?: string): Promise<unknown> {
  if (signal?.aborted) throw new AcknowledgmentCallbackFailure();
  const common = { slug: scope.app, invocationId: scope.invocationId };
  // A Blob deliberately bypasses the generated JSON serializer. Parsing and
  // re-stringifying saved JSON would round large numbers and change results.
  const pending = action === 'get' ? EventsService.getEventAcknowledgment(common)
    : action === 'renew' ? EventsService.renewEventAcknowledgment({ ...common, requestBody: { token: scope.token } })
    : action === 'complete' ? request<unknown>(OpenAPI, {
      method: 'POST', url: '/v1/apps/{slug}/event-deliveries/{invocationID}/acknowledgment/complete',
      path: { slug: scope.app, invocationID: scope.invocationId },
      body: new Blob([`{"token":${JSON.stringify(scope.token)},"result":${value}}`], { type: 'application/json' }),
    })
    : action === 'retry' ? EventsService.retryEventAcknowledgment({ ...common, requestBody: { token: scope.token, reason: value! } })
    : EventsService.deadLetterEventAcknowledgment({ ...common, requestBody: { token: scope.token, reason: value! } });
  const cancel = () => pending.cancel();
  const timer = setTimeout(cancel, settings.callbackTimeoutMs);
  signal?.addEventListener('abort', cancel, { once: true });
  try {
    if (signal?.aborted) cancel();
    return await pending;
  } catch (error) {
    if (signal?.aborted) throw new AcknowledgmentCallbackFailure();
    throw failure(error, settings);
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', cancel);
  }
}

export function acknowledgmentWait(delay: number, signal?: AbortSignal): Promise<void> {
  signal?.throwIfAborted();
  return new Promise((resolve, reject) => {
    const abort = () => { clearTimeout(timer); signal?.removeEventListener('abort', abort); reject(signal?.reason); };
    const timer = setTimeout(() => { signal?.removeEventListener('abort', abort); resolve(); }, delay);
    signal?.addEventListener('abort', abort, { once: true });
    if (signal?.aborted) abort();
  });
}
