/** Isolated, one-shot native event publication; original bytes survive outbox recovery. */
import { EventContract } from './event-contract.js';
import { jobScope, JobError } from './job-runtime.js';
import type { PipelineScope } from './pipeline-state.js';
import { EventOutboxError, outboxScope, outboxReceipt, parseOutboxRequest, prepareEventPublication,
  type EventOutboxOptions, type EventPublicationOptions, type EventPublicationOutcome } from './event-outbox-state.js';

export interface EventOutboxHTTPResult { status: number; value: unknown; retryAfter: string | null; }
export async function eventOutboxOperation<T>(options: EventOutboxOptions, work: (signal: AbortSignal, remaining: () => number) => Promise<T>): Promise<T> {
  if (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs <= 0 || options.timeoutMs > 2147483647) throw new EventOutboxError('invalid_request');
  const deadline = performance.now() + options.timeoutMs;
  try {
    return await jobScope(options.timeoutMs, options.signal, async signal => {
      const remaining = () => { signal.throwIfAborted(); const ms = Math.floor(deadline - performance.now()); if (ms < 1) throw new EventOutboxError('deadline'); return ms; };
      remaining(); const result = await work(signal, remaining); remaining(); return result;
    });
  } catch (error) {
    if (error instanceof EventOutboxError) throw error;
    if (error instanceof JobError && error.code === 'deadline') throw new EventOutboxError('deadline');
    if (options.signal?.aborted) throw new EventOutboxError('cancelled');
    throw new EventOutboxError('unverified');
  }
}
export function eventPublicationTransport(scope: PipelineScope, token: string, fetcher: typeof fetch = globalThis.fetch) {
  try { if (!token) throw new Error(); new Headers({ authorization: 'Bearer ' + token }); }
  catch { throw new EventOutboxError('invalid_request'); }
  return async (slug: string, body: string, verify: boolean, options: EventOutboxOptions): Promise<EventOutboxHTTPResult> => eventOutboxOperation(options, async signal => {
    try {
      const response = await fetcher(scope.base_url + '/v1/apps/' + encodeURIComponent(slug) + (verify ? '/events/verify-publication' : '/events:publish'), {
        method: 'POST', redirect: 'manual', headers: { authorization: 'Bearer ' + token, 'content-type': 'application/json', accept: 'application/json' }, body, signal,
      });
      signal.throwIfAborted();
      const reader = response.body?.getReader(); const chunks: Uint8Array[] = []; let length = 0;
      try { if (reader) for (;;) { const part = await reader.read(); signal.throwIfAborted(); if (part.done) break; length += part.value.length; if (length > 1048576) throw new Error(); chunks.push(part.value); } }
      finally { if (reader) { void reader.cancel().catch(() => {}); reader.releaseLock(); } }
      let value: unknown;
      try { value = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { value = null; }
      return { status: response.status, value, retryAfter: response.headers.get('retry-after') };
    } catch { signal.throwIfAborted(); return { status: 0, value: null, retryAfter: null }; }
  });
}
export function eventPublicationOutcome(response: EventOutboxHTTPResult, scope: PipelineScope, key: string): EventPublicationOutcome {
  if (response.status >= 200 && response.status < 300) {
    try { return { state: 'published', receipt: outboxReceipt(response.value, scope, key) }; } catch { /* preserve uncertainty */ }
  }
  return response.status >= 400 && response.status < 500 && ![408, 429].includes(response.status)
    ? { state: 'blocked', code: 'publication_rejected', httpStatus: response.status }
    : { state: 'unconfirmed', code: 'unconfirmed', ...(response.status ? { httpStatus: response.status } : {}) };
}
export async function publishTypedEvent<E>(contract: EventContract<E>, definition: { scope: PipelineScope; token: string; fetch?: typeof fetch }, input: E, options: EventPublicationOptions & EventOutboxOptions): Promise<EventPublicationOutcome> {
  const scope = outboxScope(definition.scope), body = prepareEventPublication(contract, scope, input, options);
  const send = eventPublicationTransport(scope, definition.token, definition.fetch);
  parseOutboxRequest(body, scope.app_id);
  return eventPublicationOutcome(await send(scope.app_slug, body, false, options), scope, options.key);
}
