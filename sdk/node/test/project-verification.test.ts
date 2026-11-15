import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createClient, parseProjectVerificationTarget, type VerificationCheck, type ProjectVerificationTarget } from '../src/index.js';
import { createClient as preview } from '../src/preview.js';
const fixture = JSON.parse(readFileSync(new URL('../../../request-contracts/project-verification.json', import.meta.url), 'utf8')) as {
    target: ProjectVerificationTarget;
    project_id: string;
    cases: {
        scenario: string;
        outcome: string;
        code: string;
    }[];
};
function server(scenario: string, observed: string[] = []): typeof fetch {
    return async (url, init) => {
        const path = new URL(String(url)).pathname.replace('/prefix', '');
        observed.push(path);
        assert.equal(init?.method, 'GET');
        assert.equal(init?.redirect, 'error');
        assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer owner');
        let data: Record<string, any>;
        const w = fixture.target.workloads.find(w => path.includes(w.slug) || path.includes(w.deployment_id!));
        const subject = w?.slug === 'shop-worker';
        if (path === '/v1/projects/shop')
            data = { slug: 'shop', id: fixture.project_id };
        else if (!w)
            throw new Error(path);
        else if (subject && scenario === 'denied')
            return Response.json({ detail: 'secret-value' }, { status: 403 });
        else if (path.endsWith('/health')) {
            const date = new Date(Date.now() - 1000).toISOString();
            data = { app_id: w.app_id, scope: 'default', status: 'healthy', phase: subject ? 'unsupported' : 'serving', evaluated_at: date, metrics_as_of: date, valid_for_seconds: 60, serving_deployment_ids: [w.deployment_id] };
            if (subject && ['stale', 'stale_sample', 'future', 'missing_metrics', 'old_release', 'mixed_release', 'unhealthy', 'wrong_scope', 'naive_timestamp', 'invalid_timestamp', 'expired_ttl', 'health_unknown'].includes(scenario))
                data.phase = 'serving';
            if (subject && scenario === 'stale')
                data.evaluated_at = data.metrics_as_of = new Date(Date.now() - 300000).toISOString();
            if (subject && scenario === 'stale_sample')
                data.metrics_as_of = new Date(Date.now() - 300000).toISOString();
            if (subject && scenario === 'future')
                data.evaluated_at = data.metrics_as_of = new Date(Date.now() + 300000).toISOString();
            if (subject && scenario === 'missing_metrics')
                delete data.metrics_as_of;
            if (subject && scenario === 'old_release')
                data.serving_deployment_ids = ['c'.repeat(32)];
            if (subject && scenario === 'mixed_release')
                data.serving_deployment_ids.push('c'.repeat(32));
            if (subject && scenario === 'unhealthy')
                data.status = 'unhealthy';
            if (subject && scenario === 'wrong_scope')
                data.scope = 'staging';
            if (subject && scenario === 'naive_timestamp')
                data.evaluated_at = data.metrics_as_of = new Date(Date.now() - 1000).toISOString().replace('Z', '');
            if (subject && scenario === 'invalid_timestamp')
                data.evaluated_at = data.metrics_as_of = '2026-02-30T00:00:00Z';
            if (subject && scenario === 'expired_ttl')
                data.valid_for_seconds = 0;
            if (subject && scenario === 'health_unknown')
                data.status = 'unknown';
        }
        else if (path.endsWith('/timeline')) {
            data = { deployment_id: w.deployment_id, app: w.slug, phase: 'released', status: 'live', traffic_percent: 100, events: [] };
            if (subject && scenario === 'release_failed') {
                data.phase = 'failed';
                data.reason = 'secret-value';
            }
            if (subject && scenario === 'approval')
                data.phase = 'awaiting_approval';
            if (subject && ['building', 'superseded'].includes(scenario))
                data.phase = scenario;
            if (subject && scenario === 'unknown_phase')
                data.phase = 'future-phase';
            if (subject && scenario === 'wrong_timeline')
                data.deployment_id = 'c'.repeat(32);
            if (subject && scenario === 'partial_traffic')
                data.traffic_percent = 30;
        }
        else if (path.startsWith('/v1/deployments/')) {
            data = { id: w.deployment_id, app_id: w.app_id, status: 'live', error_relevant_logs: Array.from({ length: 25 }, () => ({ message: 'secret-value ' + 'x'.repeat(800) })) };
            if (subject && scenario === 'wrong_deployment')
                data.app_id = 'f'.repeat(32);
            if (subject && scenario === 'oversized')
                data.padding = 'x'.repeat(300000);
        }
        else {
            data = { id: w.app_id, slug: w.slug, project_id: fixture.project_id, workload_class: subject ? 'worker' : 'http' };
            if (subject && scenario === 'wrong_app')
                data.id = 'f'.repeat(32);
            if (subject && scenario === 'wrong_project')
                data.project_id = 'f'.repeat(32);
        }
        return Response.json(data, { headers: { 'X-Request-ID': '00000000-0000-4000-8000-'+String(observed.length).padStart(12,'0') } });
    };
}
function checks(scenario: string, calls: string[] = []): VerificationCheck[] {
    return fixture.target.workloads.filter(w => !(scenario === 'unsupported_no_check' && w.slug === 'shop-worker')).map(w => ({ name: w.slug, workload: w.slug, run: context => {
            calls.push(w.slug);
            assert.ok(context.signal instanceof AbortSignal);
            assert.ok(context.remainingMs > 0);
            assert.equal(context.deployment_id, w.deployment_id);
            if (w.slug === 'shop-worker' && scenario === 'probe_exception')
                throw new Error('secret-value');
            return { outcome: w.slug === 'shop-worker' && scenario === 'failed_worker' ? 'failed' : 'verified', deployment_id: w.slug === 'shop-worker' && scenario === 'unattributed' ? 'c'.repeat(32) : w.deployment_id! };
        } }));
}
for (const [surface, factory] of [['public', createClient], ['preview', preview]] as const)
    for (const c of fixture.cases)
        test(`${surface} project verification: ${c.scenario}`, async () => {
            const paths: string[] = [], calls: string[] = [];
            const target = { ...fixture.target, workloads: fixture.target.workloads.map(w => ({ ...w })) };
            if (c.scenario === 'unchanged')
                target.workloads[1] = { ...target.workloads[1]!, deployment_id: null };
            const client = factory(target.base_url, { token: 'owner', fetch: server(c.scenario, paths) });
            const result = await client.projects.verifyApplication(target, { timeoutMs: 2000, maxPolls: 2, pollDelayMs: 0, checks: checks(c.scenario, calls), redact: text => text.replaceAll('secret-value', '[redacted]') });
            assert.equal(result.outcome, c.outcome);
            assert.equal(result.workloads[1]!.code, c.code);
            assert.equal(result.target.workloads[1]!.deployment_id, target.workloads[1]!.deployment_id);
            assert.ok(result.workloads[0]!.request_ids.length > 0);
            assert.ok(Object.isFrozen(result.workloads[0]));
            assert.ok(!JSON.stringify(result).includes('secret-value'));
            assert.ok(result.workloads.every(w => w.diagnostics.length <= 20 && w.diagnostics.every(t => t.length <= 512)));
            if (['denied', 'wrong_app', 'wrong_project', 'wrong_deployment', 'wrong_timeline', 'wrong_scope', 'oversized', 'unchanged'].includes(c.scenario))
                assert.ok(!calls.includes('shop-worker'));
            if (c.scenario === 'release_failed')
                assert.ok(result.workloads[1]!.diagnostics_truncated);
            assert.ok(paths.every(p => !p.includes('c'.repeat(32))));
        });
test('deadline, cancellation, request limits and late callbacks retain bounded partial evidence', async () => {
    const client = createClient(fixture.target.base_url, { token: 'owner', fetch: server('verified') });
    const base = { timeoutMs: 1000, maxPolls: 1, pollDelayMs: 0, checks: checks('verified') };
    const limit = await client.projects.verifyApplication(fixture.target, { ...base, maxRequests: 2 });
    assert.equal(limit.requests, 2);
    assert.equal(limit.outcome, 'inconclusive');
    const slow: VerificationCheck = { name: 'slow', workload: 'shop-worker', run: () => new Promise(() => { }) };
    const start = performance.now();
    const timed = await client.projects.verifyApplication(fixture.target, { ...base, timeoutMs: 40, checks: [...checks('verified').slice(0, 1), slow] });
    assert.equal(timed.code, 'deadline');
    assert.equal(timed.workloads[0]!.outcome, 'verified');
    assert.ok(performance.now() - start < 500);
    const controller = new AbortController(), reason = new Error('stop');
    let enter!: () => void;
    const entered = new Promise<void>(resolve => { enter = resolve; });
    const pending = client.projects.verifyApplication(fixture.target, { ...base, signal: controller.signal, checks: [{ ...slow, run: () => { enter(); return new Promise(() => { }); } }] });
    await entered;
    controller.abort(reason);
    await assert.rejects(pending, e => e === reason);
    const progress = await client.projects.verifyApplication(fixture.target, { ...base, timeoutMs: 30, onProgress: () => new Promise(() => { }) });
    assert.equal(progress.code, 'deadline');
});
test('concurrent accounts and configuration snapshots never cross ownership or pass SDK credentials to probes', async () => {
    const seen: string[] = [], transport = server('verified');
    let active = 0, peak = 0;
    const fetch: typeof globalThis.fetch = async (url, init) => { const token = new Headers(init?.headers).get('Authorization'); seen.push(token!); active++; peak = Math.max(peak, active); await new Promise(r => setTimeout(r, 2)); active--; return token === 'Bearer owner' ? transport(url, init) : Response.json({ detail: 'hidden' }, { status: 404 }); };
    const a = createClient(fixture.target.base_url, { token: 'owner', fetch }), b = createClient(fixture.target.base_url, { token: 'foreign', fetch });
    let probes = 0;
    const pending = a.projects.verifyApplication(fixture.target, { timeoutMs: 2000, maxPolls: 1, concurrency: 1, checks: checks('verified') });
    a.setToken('changed');
    a.setBaseURL('https://other.test');
    const foreign = b.projects.verifyApplication(fixture.target, { timeoutMs: 2000, maxPolls: 1, checks: [{ name: 'forbidden', workload: 'shop-api', run: () => { probes++; throw new Error(); } }] });
    const [first, second] = await Promise.all([pending, foreign]);
    assert.equal(first.outcome, 'verified');
    assert.equal(second.outcome, 'inconclusive');
    assert.equal(probes, 0);
    assert.ok(!seen.includes('Bearer changed'));
    assert.ok(peak <= 2);
    assert.throws(() => parseProjectVerificationTarget({ ...fixture.target, workloads: [...fixture.target.workloads, fixture.target.workloads[0]] }));
});
test('diagnostic text is opt-in and a failed redactor never exposes original text', async () => {
    const client = createClient(fixture.target.base_url, { token: 'owner', fetch: server('release_failed') });
    for (const redact of [undefined, () => { throw new Error('redact failed'); }]) {
        const result = await client.projects.verifyApplication(fixture.target, { timeoutMs: 1000, maxPolls: 1, redact });
        assert.ok(result.workloads.every(w => w.diagnostics.length === 0));
        assert.ok(!JSON.stringify(result).includes('secret-value'));
    }
});
test('stalled headers and bodies share the deadline; pre-cancelled calls make no reads', async () => {
    for (const body of [false, true]) {
        let requests = 0, cancelled = false;
        const client = createClient(fixture.target.base_url, { fetch: async () => { requests++; return body ? new Response(new ReadableStream({ start(c) { c.enqueue(new TextEncoder().encode('{')); }, cancel() { cancelled = true; } })) : new Promise(() => { }); } });
        const result = await client.projects.verifyApplication(fixture.target, { timeoutMs: 30 });
        assert.equal(result.code, 'deadline');
        assert.equal(requests, 1);
        if (body) assert.equal(cancelled, true);
    }
    let requests = 0;
    const c = new AbortController();
    c.abort(new Error('stop'));
    const client = createClient(fixture.target.base_url, { fetch: async () => { requests++; throw new Error(); } });
    await assert.rejects(client.projects.verifyApplication(fixture.target, { timeoutMs: 30, signal: c.signal }), e => e === c.signal.reason);
    assert.equal(requests, 0);
});
test('poll caps, explicit environment and late callbacks preserve identities and terminal reports', async () => {
    const paths: string[] = [];
    const client = createClient(fixture.target.base_url, { token: 'owner', fetch: server('building', paths) });
    const result = await client.projects.verifyApplication(fixture.target, { timeoutMs: 1000, maxPolls: 3, pollDelayMs: 0, checks: checks('verified') });
    assert.equal(result.workloads[1]!.polls, 3);
    assert.equal(paths.filter(p => p.endsWith(`${'b'.repeat(32)}/timeline`)).length, 3);
    const target = { ...fixture.target, environment: 'staging' };
    const original = server('verified');
    const scoped = createClient(target.base_url, { token: 'owner', fetch: async (url, init) => { const response = await original(url, init), value = await response.json(); if (String(url).includes('/health'))
            assert.equal(new URL(String(url)).searchParams.get('environment'), 'staging'); return Response.json({ ...value, scope: 'staging' }); } });
    assert.equal((await scoped.projects.verifyApplication(target, { timeoutMs: 1000, maxPolls: 1, checks: checks('verified') })).outcome, 'verified');
    let release!: (v: any) => void;
    const late = new Promise<any>(r => { release = r; });
    const progress: string[] = [];
    const final = await createClient(fixture.target.base_url, { token: 'owner', fetch: server('verified') }).projects.verifyApplication(fixture.target, { timeoutMs: 30, maxPolls: 2, pollDelayMs: 0, checks: [{ name: 'late', workload: 'shop-worker', run: () => late }], onProgress: r => { progress.push(r.slug); } });
    const saved = JSON.stringify(final), count = progress.length;
    release({ outcome: 'verified', deployment_id: 'b'.repeat(32) });
    await new Promise(r => setTimeout(r, 5));
    assert.equal(JSON.stringify(final), saved);
    assert.equal(progress.length, count);
});
