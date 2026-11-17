import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { EventContract } from '../src/event-contract.js';
import { createStripeWebhookAdapter, stripeDeliveryContextFromHeaders, stripeReceiptId, stripeWebhookSchema, type StripeWebhookDelivery, type StripeWebhookDecision } from '../src/stripe-webhooks.js';
import { stripeWebhookSQL as SQL } from '../src/stripe-webhook-protocol.js';
import { outboxSQL as OUTBOX } from '../src/event-outbox-protocol.js';
import { stripeHash } from '../src/stripe-webhook-state.js';
import type { EventOutboxPool } from '../src/event-outbox.js';
const f = JSON.parse(readFileSync(new URL('../../../request-contracts/stripe-webhooks.json', import.meta.url), 'utf8')), scope = f.scope, contract = new EventContract<{
    import_id: string;
}>(f.contract), settings = { timeoutMs: 1000 };
function delivery(event = f.event): StripeWebhookDelivery { const body = Buffer.from(event === f.event ? f.body : JSON.stringify(event)); return { method: 'POST', path: f.path, body, headers: { ...f.headers, 'x-faas-invocation-id': stripeReceiptId(f.endpoint_id, event.id), 'x-gregale-webhook-event-id': event.id, 'x-gregale-webhook-event-type': event.type, 'x-gregale-webhook-body-sha256': stripeHash(body) } }; }
function fixture() {
    const state = { ledger: new Map<string, any>(), outbox: new Map<string, any>(), paid: false, owner: 'alice', applies: 0, auth: 0, http: 0, commits: 0, active: false, revoked: false, cancelled: false, failApply: false, invalid: false, loseCommit: false, hangCommit: false, wrongApp: false, disabled: false, secret: '***' };
    const pool: EventOutboxPool = { async connect() {
            let ledger = state.ledger, outbox = state.outbox, paid = state.paid;
            return { async query(sql, values = []) {
                    const key = values.slice(0, 4).join(':');
                    if (sql.startsWith('BEGIN')) {
                        ledger = structuredClone(state.ledger);
                        outbox = structuredClone(state.outbox);
                        paid = state.paid;
                        state.active = true;
                        return { rows: [] };
                    }
                    if (sql.startsWith('SELECT set_config') || sql === SQL.lock)
                        return { rows: [] };
                    if (sql === 'ROLLBACK') {
                        state.active = false;
                        return { rows: [] };
                    }
                    if (sql === 'COMMIT') {
                        state.ledger = ledger;
                        state.outbox = outbox;
                        state.paid = paid;
                        state.commits++;
                        state.active = false;
                        if (state.hangCommit)
                            return new Promise(() => { });
                        if (state.loseCommit)
                            throw Error('private commit diagnostics');
                        return { rows: [] };
                    }
                    if (sql === SQL.reserve) {
                        if (ledger.has(key))
                            return { rows: [] };
                        ledger.set(key, { identity: values[4], binding_digest: values[5], body_digest: values[6], state: 'processing', owner_id: null, outbox_key: null, reason: '', created_at: '2026-11-16T10:00:00.123456Z', updated_at: '2026-11-16T10:00:00.123456Z' });
                        return { rows: [{ event_id: values[3] }] };
                    }
                    if (sql === SQL.load)
                        return { rows: ledger.has(key) ? [ledger.get(key)] : [] };
                    if (sql === SQL.complete) {
                        const row = { ...ledger.get(key), state: values[4], owner_id: values[5], outbox_key: values[6], reason: values[7] };
                        ledger.set(key, row);
                        return { rows: [row] };
                    }
                    if (sql === 'business.read')
                        return { rows: [{ paid, owner: state.owner, cancelled: state.cancelled }] };
                    if (sql === 'business.write') {
                        paid = true;
                        return { rows: [] };
                    }
                    const outkey = values.slice(0, 3).join(':');
                    if (sql === OUTBOX.stage) {
                        outbox.set(outkey, { request_digest: values[4], app_slug: values[3] });
                        return { rows: [] };
                    }
                    if (sql === OUTBOX.stage_check)
                        return { rows: [outbox.get(outkey)] };
                    if (sql === OUTBOX.status)
                        return { rows: [{ producer_key: values[2], app_slug: scope.app_slug, state: 'pending', attempts: '0', next_attempt_at: '2026-11-16T10:00:00Z', retry_until: '2026-11-17T10:00:00Z', created_at: '2026-11-16T10:00:00Z', updated_at: '2026-11-16T10:00:00Z', claim_expires_at: null, accepted_at: null, last_code: '' }] };
                    throw Error('unexpected SQL');
                }, release() { state.active = false; } };
        } };
    const definition = { name: 'paid-imports', scope, endpointId: f.endpoint_id, deliveryPath: f.path, apiVersion: f.api_version, livemode: false, eventTypes: ['payment_intent.succeeded', 'payment_intent.payment_failed'], pool, token: 'backend', fetch: async (url: any, init: any) => { state.http++; assert.equal(state.active, false); assert.equal(new Headers(init.headers).get('authorization'), 'Bearer backend'); const path = new URL(String(url)).pathname; return Response.json(path === '/v1/account' ? { id: scope.account_id } : path.endsWith('/inbound-webhooks/' + f.endpoint_id) ? { id: f.endpoint_id, account_id: scope.account_id, app_id: scope.app_id, provider: 'stripe', delivery_path: f.path, enabled: !state.disabled, signing_secret_masked: state.secret } : { id: state.wrongApp ? f.endpoint_id : scope.app_id }); }, verify: async (d: StripeWebhookDelivery) => stripeDeliveryContextFromHeaders(d.headers, d.body, scope.account_id, scope.app_id), authorize: async (tx: any, event: any, actor: string) => { state.auth++; await tx.query('business.read'); return actor === 'alice' && !state.revoked ? state.owner : null; }, apply: async (tx: any, event: any): Promise<StripeWebhookDecision<{
            import_id: string;
        }>> => { state.applies++; if (state.failApply)
            throw Error('private customer payload'); const row = (await tx.query('business.read')).rows[0]; if (row.cancelled)
            return { state: 'ignore', reason: 'cancelled' }; if (row.paid)
            return { state: 'ignore', reason: event.type === 'payment_intent.succeeded' ? 'already_applied' : 'superseded' }; if (event.type !== 'payment_intent.succeeded')
            return { state: 'ignore', reason: 'not_applicable' }; await tx.query('business.write'); return { state: 'stage', input: state.invalid ? { import_id: 7 as any } : { import_id: f.import_id } }; } };
    return { state, definition, adapter: createStripeWebhookAdapter(contract, definition) };
}
test('native UUID/body vectors and shared SQL schema agree', () => { assert.equal(stripeReceiptId(f.endpoint_id, f.event.id), f.receipt_id); assert.equal(stripeHash(stripeWebhookSchema), f.schema_sha256); assert.deepEqual(SQL, JSON.parse(readFileSync(new URL('../../../request-contracts/stripe-webhook-sql.json', import.meta.url), 'utf8'))); const c = stripeDeliveryContextFromHeaders(f.headers, Buffer.from(f.body), scope.account_id, scope.app_id); assert.equal(c.body_sha256, f.body_sha256); assert.throws(() => stripeDeliveryContextFromHeaders(f.headers, Buffer.from('{}'), scope.account_id, scope.app_id), { code: 'invalid_delivery' }); });
test('business write and typed event stage once, duplicate reauthorizes, receipt is payload-free', async () => { const { adapter, state } = fixture(); const first = await adapter.handle(delivery(), 'alice', settings), again = await adapter.handle(delivery(), 'alice', settings); assert.equal(first.outcome, 'staged'); assert.equal(first.identity, f.identity); assert.equal(again.replayed, true); assert.equal(state.applies, 1); assert.equal(state.paid, true); assert.equal(state.outbox.size, 1); assert.equal(state.auth, 2); assert.ok(!JSON.stringify(first).includes('data_object')); assert.equal((await adapter.inspect(delivery(), 'alice', settings))!.created_at, '2026-11-16T10:00:00.123456Z'); const row = [...state.ledger.values()][0]; assert.equal(Buffer.from(row.binding_digest).toString('hex'), f.binding_digest); });
test('distinct provider events for same business effect and reordered failure do not repeat writes', async () => { const { adapter, state } = fixture(); await adapter.handle(delivery(), 'alice', settings); assert.equal((await adapter.handle(delivery({ ...f.event, id: 'evt_second' }), 'alice', settings)).reason, 'already_applied'); assert.equal((await adapter.handle(delivery({ ...f.event, id: 'evt_old', type: 'payment_intent.payment_failed', created: f.event.created - 1 }), 'alice', settings)).reason, 'superseded'); assert.equal(state.outbox.size, 1); });
test('current owner authorization ignores provider owner claims and prevents revoked/changed-owner duplicates', async () => { const { adapter, state } = fixture(); await assert.rejects(adapter.handle(delivery(), 'bob', settings), { code: 'unauthorized' }); assert.equal(state.ledger.size, 0); await adapter.handle(delivery(), 'alice', settings); state.revoked = true; await assert.rejects(adapter.handle(delivery(), 'alice', settings), { code: 'unauthorized' }); state.revoked = false; state.owner = 'bob'; await assert.rejects(adapter.inspect(delivery(), 'alice', settings), { code: 'unauthorized' }); assert.equal(state.applies, 1); });
test('business cancellation is durable and schema/callback failures roll back everything', async () => { for (const mode of ['cancelled', 'invalid', 'failApply'] as const) {
    const { adapter, state } = fixture();
    state[mode] = true;
    if (mode === 'cancelled') {
        const first = await adapter.handle(delivery(), 'alice', settings);
        assert.equal(first.reason, 'cancelled');
        assert.equal((await adapter.handle(delivery(), 'alice', settings)).replayed, true);
        assert.equal(state.outbox.size, 0);
    }
    else {
        await assert.rejects(adapter.handle(delivery(), 'alice', settings), (e: any) => e.code === 'store_failed' && !e.message.includes('private'));
        assert.equal(state.paid, false);
        assert.equal(state.ledger.size, 0);
        assert.equal(state.outbox.size, 0);
    }
} });
test('uncertain commit is inspectable without repeating business effects', async () => { for (const hang of [false, true]) {
    const { adapter, state } = fixture();
    state.loseCommit = !hang;
    state.hangCommit = hang;
    await assert.rejects(adapter.handle(delivery(), 'alice', { timeoutMs: hang ? 30 : 1000 }), (e: any) => e.code === 'commit_unknown' && e.identity === f.identity);
    state.loseCommit = state.hangCommit = false;
    assert.equal((await adapter.inspect(delivery(), 'alice', settings))!.state, 'staged');
    assert.equal((await adapter.handle(delivery(), 'alice', settings)).replayed, true);
    assert.equal(state.applies, 1);
} });
test('version/mode/endpoint/context mismatches fail before business callbacks', async () => { for (const patch of [{ api_version: '2026-01-01' }, { livemode: true }, { data: { object: {} } }]) {
    const { adapter, state } = fixture();
    await assert.rejects(adapter.handle(delivery({ ...f.event, ...patch }), 'alice', settings));
    assert.equal(state.applies, 0);
    assert.equal(state.ledger.size, 0);
} const { adapter, state } = fixture(); state.wrongApp = true; await assert.rejects(adapter.handle(delivery(), 'alice', settings), { code: 'scope_mismatch' }); state.wrongApp = false; state.disabled = true; await assert.rejects(adapter.handle(delivery(), 'alice', settings), { code: 'endpoint_unavailable' }); });
test('same provider ID with changed bytes or changed adapter config conflicts', async () => { const { adapter, definition, state } = fixture(); await adapter.handle(delivery(), 'alice', settings); await assert.rejects(adapter.handle(delivery({ ...f.event, created: f.event.created + 1 }), 'alice', settings), { code: 'conflict' }); await assert.rejects(createStripeWebhookAdapter(contract, { ...definition, name: 'changed' }).handle(delivery(), 'alice', settings), { code: 'conflict' }); assert.equal(state.applies, 1); });
test('unsupported types and secret rotation produce no duplicate work', async () => { const { adapter, state } = fixture(); assert.equal((await adapter.handle(delivery({ ...f.event, id: 'evt_other', type: 'customer.created' }), 'alice', settings)).reason, 'event_type'); assert.equal(state.ledger.size, 0); await adapter.handle(delivery(), 'alice', settings); state.secret = '***'; assert.equal((await adapter.handle(delivery(), 'alice', settings)).replayed, true); state.disabled = true; assert.equal((await adapter.inspect(delivery(), 'alice', settings))!.state, 'staged'); });
test('native replay lineage preserves business identity', async () => { const d = delivery(), headers = { ...d.headers, 'x-faas-invocation-source': 'replay', 'x-faas-invocation-id': f.endpoint_id, 'x-faas-invocation-replay-root': f.receipt_id, 'x-faas-invocation-replay-generation': '9007199254740993' }; assert.equal(stripeDeliveryContextFromHeaders(headers, d.body, scope.account_id, scope.app_id).receipt_id, f.receipt_id); assert.throws(() => stripeDeliveryContextFromHeaders({ ...headers, 'x-faas-invocation-replay-root': f.endpoint_id }, d.body, scope.account_id, scope.app_id), { code: 'invalid_delivery' }); });
test('deadline/cancellation fences a late verifier before HTTP or SQL', async () => { for (const cancel of [false, true]) {
    const { definition, state } = fixture();
    let finish!: (v: any) => void;
    const controller = new AbortController();
    const adapter = createStripeWebhookAdapter(contract, { ...definition, verify: async () => new Promise<any>(r => finish = r) }), pending = adapter.handle(delivery(), 'alice', { timeoutMs: cancel ? 1000 : 10, signal: controller.signal });
    await new Promise(r => setTimeout(r, 1));
    if (cancel)
        controller.abort();
    await assert.rejects(pending, { code: cancel ? 'cancelled' : 'deadline' });
    finish(stripeDeliveryContextFromHeaders(f.headers, Buffer.from(f.body), scope.account_id, scope.app_id));
    await new Promise(r => setTimeout(r, 1));
    assert.equal(state.http, 0);
    assert.equal(state.ledger.size, 0);
} });
