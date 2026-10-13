import { createHash } from 'node:crypto';
import { planCommitRecovery } from './commit-recovery-plan.js';
import type { CommitRecoveryPlan } from './commit-recovery-plan.js';
import { getCommitDeliveryReport } from './commit-delivery-report.js';
import { request } from './generated/core/request.js';
import { OpenAPI } from './generated/core/OpenAPI.js';

export interface CommitRecoveryApplyItem {
  event_id: string; subscription_id: string; status: string; reason?: string; result?: unknown;
}
export interface CommitRecoveryApplyResult {
  source_id: string; items: CommitRecoveryApplyItem[]; counts: Record<string, number>; complete: boolean;
}
export class CommitRecoveryApplyError extends Error {
  constructor(readonly result: CommitRecoveryApplyResult, cause: unknown) {
    super('Commit recovery apply stopped; inspect per-item results before retrying', { cause });
    this.name = 'CommitRecoveryApplyError';
  }
}
function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') return `{${Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`).join(',')}}`;
  return JSON.stringify(value ?? null);
}
/** Sequential recovery; no automatic retries, unguarded routing writes, or saved URL dispatch. */
export async function applyCommitRecovery(source: string, reviewed: CommitRecoveryPlan,
  options: { limit?: number; recipientPages?: number; timeoutMs?: number; signal?: AbortSignal } = {}): Promise<CommitRecoveryApplyResult> {
  const { limit = 50, recipientPages = 1, timeoutMs = 120000, signal } = options;
  const plan = structuredClone(reviewed);
  const out: CommitRecoveryApplyResult = { source_id: source, items: [], counts: Object.create(null), complete: false };
  if (!Number.isInteger(limit) || limit < 1 || limit > 50 || !Number.isInteger(recipientPages) || recipientPages < 1 || recipientPages > 20 || !Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 86400000 || plan.report.source_id !== source || plan.items.length > 200 * 200 * 20 || plan.report.items.length > 200) throw new TypeError('invalid apply budgets or plan');
  const publications = new Map(plan.report.items.map(item => [item.acceptance.event_id.toLowerCase(), item]));
  const seen = new Set<string>();
  const uuid = /^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
  for (const item of plan.items) {
    const key = `${item.event_id}:${item.subscription_id}`.toLowerCase();
    if (!publications.has(item.event_id.toLowerCase()) || !uuid.test(item.subscription_id) || seen.has(key) || (plan.report.subscription_id && item.subscription_id.toLowerCase() !== plan.report.subscription_id.toLowerCase()) || !['eligible', 'skipped'].includes(item.status) || (item.status === 'eligible' && !item.action)) throw new TypeError('invalid reviewed selection');
    seen.add(key);
  }
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal?.addEventListener('abort', abort, { once: true }); if (signal?.aborted) abort();
  const timer = setTimeout(abort, timeoutMs);
  let used = 0, stopped = false, cause: unknown;
  try {
    // Validate the complete saved selection and ownership before any write.
    await getCommitDeliveryReport(source, { refresh: plan.report, recipientPages: 1, timeoutMs, signal: controller.signal });
    for (const item of plan.items) {
      const result: CommitRecoveryApplyItem = { event_id: item.event_id, subscription_id: item.subscription_id, status: 'skipped', reason: 'not_reviewed_eligible' };
      if (stopped) { result.status = 'not_attempted'; result.reason = 'batch_stopped'; }
      else if (item.status === 'eligible') {
        if (used >= limit) { result.status = 'not_attempted'; result.reason = 'apply_budget'; }
        else {
          used++;
          if (controller.signal.aborted) { result.status = 'not_attempted'; result.reason = 'interrupted'; stopped = true; cause = new Error('apply interrupted'); }
          else {
            const original = publications.get(item.event_id.toLowerCase())!;
            let current: CommitRecoveryPlan | undefined;
            try { current = await planCommitRecovery(source, { ...plan.report, subscription_id: item.subscription_id.toLowerCase(), items: [{ ...original, subscribers: [] }] }, { recipientPages, timeoutMs, signal: controller.signal }); }
            catch (error) { result.status = 'read_error'; result.reason = 'eligibility_read_failed'; if (controller.signal.aborted) { stopped = true; cause = error; } }
            if (current) {
              const fresh = current.items[0];
              if (!fresh) result.reason = current.report.items[0]?.evidence_status ?? 'evidence_unavailable';
              else if (item.action?.kind === 'routing_replay' && !Number.isSafeInteger(item.action.body?.expected_generation)) result.reason = 'generation_guard_unavailable';
              else if (item.action?.kind === 'routing_replay' && current.report.items[0]?.subscribers[0]?.recipient?.routing?.generation === undefined) result.reason = 'generation_guard_unavailable';
              else if (item.action?.kind === 'routing_replay' && item.action.body?.expected_generation !== current.report.items[0]?.subscribers[0]?.recipient?.routing?.generation) result.reason = 'generation_changed';
              else if (fresh.status !== 'eligible') result.reason = fresh.reason;
              else if (canonical(item.action) !== canonical(fresh.action)) result.reason = 'action_changed';
              else if (fresh.action?.kind === 'routing_replay' && !fresh.action.body?.expected_accepted_at) result.reason = 'acceptance_guard_unavailable';
              else if (fresh.action) {
                // Only fresh planner-validated execution-scoped paths are dispatched.
                let identity = fresh.action.kind + '\n' + fresh.action.url;
                const routing = fresh.action.kind === 'routing_replay';
                const publication = original.acceptance.publication!;
                if (routing) identity += '\n' + publication.source + '\n' + publication.id + '\n' + item.subscription_id.toLowerCase() + '\n' + fresh.action.body!.expected_accepted_at + '\n' + fresh.action.body!.expected_generation;
                const idempotencyKey = 'commit-recovery-' + createHash('sha256').update(identity).digest('hex');
                const post = request<unknown>(OpenAPI, { method: 'POST', url: fresh.action.url, body: routing ? fresh.action.body : undefined, mediaType: routing ? 'application/json' : undefined, headers: { 'Idempotency-Key': idempotencyKey } });
                const cancel = () => post.cancel(); controller.signal.addEventListener('abort', cancel, { once: true });
                try {
                  if (controller.signal.aborted) { post.cancel(); throw new Error('apply interrupted'); }
                  const response = await post;
                  if (routing) {
                    const ack = response as { event_source?: string; event_id?: string; subscription_id?: string; state?: string } | undefined;
                    if (!ack || ack.event_source !== publication.source || ack.event_id !== publication.id || ack.subscription_id?.toLowerCase() !== item.subscription_id.toLowerCase() || ack.state !== 'pending') throw new Error('routing recovery acknowledgement unavailable');
                  } else if (!response || typeof response !== 'object' || typeof (response as { id?: unknown }).id !== 'string' || !(response as { id: string }).id) throw new Error('recovery acknowledgement unavailable');
                  result.result = response; result.status = 'queued'; delete result.reason;
                } catch (error) {
                  const status = (error as { status?: number }).status;
                  if (status === 409 && (error as { body?: { code?: string } }).body?.code === 'event_replay_generation_changed') { result.status = 'skipped'; result.reason = 'generation_changed'; }
                  else if (status && status >= 400 && status < 500 && status !== 408) { result.status = 'rejected'; result.reason = `http_${status}`; }
                  else { result.status = 'unknown'; result.reason = 'inspect_before_retry'; stopped = true; cause = error; }
                } finally { controller.signal.removeEventListener('abort', cancel); }
              }
            }
          }
        }
      }
      out.items.push(result); out.counts[result.status] = (out.counts[result.status] ?? 0) + 1;
    }
    out.complete = !stopped;
    if (stopped) throw new CommitRecoveryApplyError(out, cause);
    return out;
  } catch (error) {
    if (error instanceof CommitRecoveryApplyError) throw error;
    for (const item of plan.items.slice(out.items.length)) {
      out.items.push({ event_id: item.event_id, subscription_id: item.subscription_id, status: 'not_attempted', reason: 'batch_stopped' });
      out.counts.not_attempted = (out.counts.not_attempted ?? 0) + 1;
    }
    throw new CommitRecoveryApplyError(out, error);
  }
  finally { clearTimeout(timer); signal?.removeEventListener('abort', abort); }
}
