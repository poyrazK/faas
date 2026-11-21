/** Verified Stripe deliveries -> one atomic business decision and typed outbox intent. */
import { EventContract } from './event-contract.js';
import { JobError, jobScope, jobSnapshot, jobValueHash } from './job-runtime.js';
import { eventPipelineScope } from './event-pipeline-state.js';
import { eventConsumerUUID } from './event-consumer-transactions.js';
import { createEventPublisher, withEventOutboxTransaction, type EventOutboxPool, type EventOutboxTransaction } from './event-outbox.js';
import { EventOutboxError } from './event-outbox-state.js';
import { eventOutboxTransactionQuery } from './event-outbox-transaction.js';
import { pipelineName, type PipelineScope } from './pipeline-state.js';
import { stripeWebhookSQL as SQL, stripeWebhookSchema } from './stripe-webhook-protocol.js';
import { StripeWebhookError, stripePath, stripeVersion, stripeEventType, stripeHash, stripeReceiptId, captureStripeDelivery, stripeSnapshot, stripeWebhookIdentity, stripeMetadata, type StripeWebhookDelivery, type StripeDeliveryContext, type StripeWebhookEvent, type StripeWebhookDecision, type StripeWebhookReceipt, type StripeWebhookOutcome } from './stripe-webhook-state.js';
export { stripeWebhookSchema };
export * from './stripe-webhook-state.js';
export interface StripeWebhookOptions {
    timeoutMs: number;
    signal?: AbortSignal;
}
export type StripeWebhookAction = 'handle' | 'inspect';
export interface StripeWebhookDefinition<E, A> {
    name: string;
    scope: PipelineScope;
    endpointId: string;
    deliveryPath: string;
    apiVersion: string;
    livemode: boolean;
    connectedAccountId?: string;
    eventTypes: readonly string[];
    pool: EventOutboxPool;
    token: string;
    fetch?: typeof fetch;
    retryWindowMs?: number;
    /** Authenticate the platform route before parsing its protected delivery context. */
    verify: (delivery: StripeWebhookDelivery, signal: AbortSignal) => Promise<StripeDeliveryContext>;
    /** Current business-owner lookup/authorization. SQL only; lock the business row for handle. */
    authorize: (tx: EventOutboxTransaction, event: StripeWebhookEvent, access: A, action: StripeWebhookAction, signal: AbortSignal) => Promise<string | null>;
    /** Runs only for a new receipt. Business SQL and returned event intent commit together. */
    apply: (tx: EventOutboxTransaction, event: StripeWebhookEvent, ownerId: string, access: A, signal: AbortSignal) => Promise<StripeWebhookDecision<E>>;
}
export interface StripeWebhookAdapter<A> {
    handle(delivery: StripeWebhookDelivery, access: A, options: StripeWebhookOptions): Promise<StripeWebhookOutcome>;
    inspect(delivery: StripeWebhookDelivery, access: A, options: StripeWebhookOptions): Promise<StripeWebhookReceipt | null>;
}
export function createStripeWebhookAdapter<E, A>(contract: EventContract<E>, definition: StripeWebhookDefinition<E, A>): StripeWebhookAdapter<A> {
    const { pool, token, verify, authorize, apply } = definition, fetcher = definition.fetch ?? globalThis.fetch;
    const scope = eventPipelineScope(definition.scope), endpoint = eventConsumerUUID(definition.endpointId), path = definition.deliveryPath, version = definition.apiVersion, live = definition.livemode, account = definition.connectedAccountId;
    const types = jobSnapshot([...definition.eventTypes]), window = definition.retryWindowMs ?? 86400000;
    if (!pipelineName(definition.name) || !stripePath(path) || !stripeVersion(version) || typeof live !== 'boolean' || account !== undefined && !/^acct_[A-Za-z0-9]{1,250}$/.test(account) || !types.length || types.length > 64 || types.some(t => !stripeEventType(t)) || new Set(types).size !== types.length || !Number.isSafeInteger(window) || window < 1000 || window > 86400000 || !token)
        throw new StripeWebhookError('invalid_delivery');
    const captured = new EventContract<E>(contract.spec), binding = Buffer.from(jobValueHash(['gregale.stripe-webhook-binding.v1', scope, endpoint, definition.name, path, version, live, account ?? null, [...types].sort(), captured.sha256, window]), 'hex');
    const publisher = createEventPublisher(captured, { scope });
    async function run<T>(delivery: StripeWebhookDelivery, access: A, settings: StripeWebhookOptions, action: StripeWebhookAction): Promise<T> {
        let identity: string | undefined, transactionPending = false;
        const saved = captureStripeDelivery(delivery), end = performance.now() + settings.timeoutMs;
        try {
            return await jobScope(settings.timeoutMs, settings.signal, async (signal) => {
                const check = () => {
                    signal.throwIfAborted();
                    const left = Math.floor(end - performance.now());
                    if (left < 1)
                        throw new StripeWebhookError('deadline', identity);
                    return left;
                };
                check();
                if (saved.path !== path)
                    throw new StripeWebhookError('scope_mismatch');
                let proof: StripeDeliveryContext;
                try {
                    proof = jobSnapshot(await verify(captureStripeDelivery(saved), signal));
                    check();
                }
                catch {
                    check();
                    throw new StripeWebhookError('unauthorized');
                }
                if (proof.account_id !== scope.account_id || proof.app_id !== scope.app_id || proof.endpoint_id !== endpoint || proof.body_sha256 !== stripeHash(saved.body) || proof.receipt_id !== stripeReceiptId(endpoint, proof.event_id))
                    throw new StripeWebhookError('scope_mismatch');
                const event = stripeSnapshot(saved.body, proof, version, live, account);
                identity = stripeWebhookIdentity(scope, endpoint, event.id);
                const id = identity, args = [scope.account_id, scope.app_id, endpoint, event.id];
                const read = async (resource: string) => {
                    check();
                    const response = await fetcher(scope.base_url + resource, { headers: { authorization: 'Bearer ' + token, accept: 'application/json' }, redirect: 'manual', signal });
                    check();
                    if (!response.ok) {
                        void response.body?.cancel().catch(() => { });
                        throw new StripeWebhookError('scope_mismatch', id);
                    }
                    const reader = response.body?.getReader();
                    let size = 0;
                    const chunks: Uint8Array[] = [];
                    try {
                        if (reader)
                            for (;;) {
                                const p = await reader.read();
                                check();
                                if (p.done)
                                    break;
                                size += p.value.length;
                                if (size > 1048576)
                                    throw new Error();
                                chunks.push(p.value);
                            }
                    }
                    finally {
                        if (reader) {
                            void reader.cancel().catch(() => { });
                            reader.releaseLock();
                        }
                    }
                    return JSON.parse(Buffer.concat(chunks).toString('utf8')) as Record<string, unknown>;
                };
                if ((await read('/v1/account')).id !== scope.account_id || (await read('/v1/apps/' + encodeURIComponent(scope.app_slug))).id !== scope.app_id)
                    throw new StripeWebhookError('scope_mismatch', id);
                if (action === 'handle') {
                    const meta = await read('/v1/apps/' + encodeURIComponent(scope.app_slug) + '/inbound-webhooks/' + endpoint);
                    if (meta.id !== endpoint || meta.account_id !== scope.account_id || meta.app_id !== scope.app_id || meta.provider !== 'stripe' || meta.delivery_path !== path)
                        throw new StripeWebhookError('scope_mismatch', id);
                    if (meta.enabled !== true)
                        throw new StripeWebhookError('endpoint_unavailable', id);
                }
                if (!types.includes(event.type)) {
                    if (action === 'inspect')
                        return null as T;
                    return jobSnapshot({ outcome: 'ignored', identity: id, replayed: false, receipt: null, reason: 'event_type' }) as T;
                }
                transactionPending = true;
                try {
                    return await withEventOutboxTransaction(pool, async (tx) => {
                        const query = eventOutboxTransactionQuery(tx);
                        await query(SQL.lock, ['gregale.stripe-webhook.v1:' + id]);
                        check();
                        let created = false;
                        if (action === 'handle')
                            created = (await query(SQL.reserve, [...args, id, binding, Buffer.from(proof.body_sha256, 'hex')])).rows.length === 1;
                        const rows = (await query(SQL.load, args)).rows;
                        if (rows.length > 1)
                            throw new EventOutboxError('store_failed');
                        const old = rows[0];
                        if (old && (!bytes(old.binding_digest, binding) || !bytes(old.body_digest, Buffer.from(proof.body_sha256, 'hex')) || old.identity !== id))
                            throw new EventOutboxError('conflict');
                        let owner: string | null;
                        try {
                            owner = await authorize(tx, jobSnapshot(event), access, action, signal);
                            check();
                        }
                        catch {
                            check();
                            throw new EventOutboxError('scope_mismatch');
                        }
                        if (!pipelineName(owner) || old?.owner_id != null && old.owner_id !== owner)
                            throw new EventOutboxError('scope_mismatch');
                        if (action === 'inspect')
                            return (old ? stripeMetadata(old, id, endpoint, event.id) : null) as T;
                        if (!created) {
                            if (!old)
                                throw new EventOutboxError('store_failed');
                            const receipt = stripeMetadata(old, id, endpoint, event.id);
                            return { outcome: receipt.state, replayed: true, identity: id, receipt, ...(receipt.reason ? { reason: receipt.reason } : {}) } as T;
                        }
                        const decision = await apply(tx, jobSnapshot(event), owner, access, signal);
                        check();
                        let key: string | null = null, reason = '';
                        if (decision.state === 'stage') {
                            key = 'stripe-webhook:' + id;
                            await publisher.stage(tx, decision.input, { key, retryWindowMs: window });
                            check();
                        }
                        else if (decision.state === 'ignore' && ['not_applicable', 'already_applied', 'cancelled', 'superseded'].includes(decision.reason))
                            reason = decision.reason;
                        else
                            throw new EventOutboxError('invalid_request');
                        const state = key ? 'staged' : 'ignored', completed = (await query(SQL.complete, [...args, state, owner, key, reason])).rows;
                        if (completed.length !== 1)
                            throw new EventOutboxError('store_failed');
                        const receipt = stripeMetadata(completed[0]!, id, endpoint, event.id);
                        return { outcome: state, identity: id, replayed: false, receipt, ...(reason ? { reason } : {}) } as T;
                    }, { timeoutMs: check(), signal });
                }
                finally {
                    transactionPending = false;
                }
            });
        }
        catch (error) {
            if (transactionPending)
                throw new StripeWebhookError('commit_unknown', identity);
            if (error instanceof StripeWebhookError)
                throw error;
            if (error instanceof EventOutboxError) {
                const code = error.code === 'scope_mismatch' ? 'unauthorized' : error.code === 'commit_unknown' ? 'commit_unknown' : error.code === 'conflict' ? 'conflict' : error.code === 'deadline' ? 'deadline' : error.code === 'cancelled' ? 'cancelled' : 'store_failed';
                throw new StripeWebhookError(code, identity);
            }
            if (settings.signal?.aborted)
                throw new StripeWebhookError('cancelled', identity);
            if (error instanceof JobError && error.code === 'deadline')
                throw new StripeWebhookError('deadline', identity);
            throw new StripeWebhookError('store_failed', identity);
        }
    }
    return Object.freeze({ handle: (delivery, access, settings) => run<StripeWebhookOutcome>(delivery, access, settings, 'handle'), inspect: (delivery, access, settings) => run<StripeWebhookReceipt | null>(delivery, access, settings, 'inspect') } satisfies StripeWebhookAdapter<A>);
}
function bytes(value: unknown, wanted: Buffer): boolean { return value instanceof Uint8Array && Buffer.from(value).equals(wanted); }
