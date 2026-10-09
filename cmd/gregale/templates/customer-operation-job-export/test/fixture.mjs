// Portable transport fixture. Native task exit, provider behavior and KVM
// qualification are covered separately; these checks use the installed SDK.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {createExportServer} from '../server.mjs';

export const ids = Object.fromEntries(['account', 'app', 'tenant', 'operation', 'run', 'instance', 'bucket', 'artifact', 'definition'].map((name, n) => [name, `${n + 1}`.repeat(8) + '-' + `${n + 1}`.repeat(4) + '-4' + `${n + 1}`.repeat(3) + '-8' + `${n + 1}`.repeat(3) + '-' + `${n + 1}`.repeat(12)]));
export const config = {apiURL: 'https://api.example.test', appID: ids.app, scope: 'production', definitionID: ids.definition};
export const env = {
  GREGALE_CUSTOMER_OPERATION_ACCOUNT_ID: ids.account, GREGALE_CUSTOMER_OPERATION_APP_ID: ids.app,
  GREGALE_CUSTOMER_OPERATION_PLATFORM_TENANT_ID: ids.tenant, GREGALE_CUSTOMER_OPERATION_SCOPE: config.scope,
  GREGALE_CUSTOMER_OPERATION_ID: ids.operation, GREGALE_RUN_ID: ids.run, GREGALE_CUSTOMER_OPERATION_JOB_INSTANCE_ID: ids.instance,
  GREGALE_CUSTOMER_OPERATION_GENERATION: '1', GREGALE_TASK_ATTEMPT: '1', GREGALE_TASK_INDEX: '0',
  GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY: 'a'.repeat(64), GREGALE_CUSTOMER_OPERATION_INPUT: '{"count":2}',
};

export async function fixture(t, options = {}) {
  const state = {writes: 0, controls: 0, receipts: 0, preparations: [], results: [], progress: [], submissions: [], cancelled: false,
    generation: 1, published: false, delivery: {state: 'pending', attempts: 0}, ...options};
  const expiry = new Date(Date.now() + 60000).toISOString();
  const snapshot = () => ({id: ids.operation, name: 'customer-export', generation: state.generation, state: state.published ? 'succeeded' : 'running',
    ...(state.published ? {result: state.result, artifacts: [state.artifact]} : {}),
    completion_delivery: state.delivery, cancellation_requested: state.cancelled, latest_sequence: 3,
    created_at: '2026-10-06T12:00:00Z', updated_at: '2026-10-06T12:01:00Z', expires_at: '2026-10-13T12:01:00Z'});
  const statusURL = `/v1/platform-tenant-self/customer-operations/${ids.operation}`;
  const receipt = {id: ids.operation, status_url: statusURL, events_url: `${statusURL}/events`};
  const fetchImpl = async (value, init = {}) => {
    const url = new URL(value), headers = new Headers(init.headers);
    assert.equal(init.redirect, 'error');
    if (url.pathname.endsWith('/control')) {
      state.controls++;
      assert.equal(headers.get('authorization'), null);
      if (headers.get('X-Gregale-Operation-Job-Capability') !== env.GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY || state.denyControl) return Response.json({code: 'operation_execution_stale'}, {status: 409});
      return Response.json({account_id: ids.account, app_id: ids.app, platform_tenant_id: ids.tenant, scope: config.scope,
        operation_id: ids.operation, job_run_id: ids.run, generation: state.generation, attempt: 1, cancellation_requested: state.cancelled,
        observed_at: new Date().toISOString(), deadline_at: expiry, lease_expires_at: expiry, poll_after_ms: 100, ...state.controlOverride});
    }
    if (url.pathname.includes('/runtime/job-operations/')) {
      assert.equal(headers.get('authorization'), null);
      assert.equal(headers.get('X-Gregale-Operation-Job-Capability'), env.GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY);
      if (url.pathname.endsWith('/artifact-uploads')) {
        assert.equal(headers.get('content-type'), 'application/octet-stream');
        assert.equal(init.credentials, 'omit');
        state.writes++;
        const bytes = Buffer.from(init.body), declaration = Object.fromEntries(url.searchParams);
        state.preparations.push(declaration);
        assert.equal(Number(declaration.size_bytes), bytes.length);
        assert.equal(declaration.sha256, 'sha256:' + createHash('sha256').update(bytes).digest('hex'));
        if (state.failTransfer && state.writes === 1) throw new TypeError('platform transfer interrupted');
        state.artifact = {id: ids.artifact, uri: `operation://${ids.operation}/artifacts/${ids.artifact}`,
          name: declaration.name, size_bytes: bytes.length, sha256: declaration.sha256};
        state.retained = bytes;
        if (state.lostPreparation && state.writes === 1) throw new TypeError('private-copy acknowledgement lost');
        return Response.json({available: true, artifact: state.artifact});
      }
      const report = JSON.parse(init.body);
      if (url.pathname.endsWith('/progress')) { state.progress.push(report); return Response.json({id: ids.operation}); }
      if (url.pathname.endsWith('/artifact-upload-receipts')) {
        state.receipts++;
        if (state.cancelAfterLookup) state.cancelled = true;
        return Response.json(state.artifact ? {available: true, artifact: state.artifact} : {available: false});
      }
      if (url.pathname.endsWith('/result')) {
        state.results.push(report); state.result = report.result;
        if (state.lostResult && state.results.length === 1) throw new TypeError('result acknowledgement lost');
        return Response.json({id: ids.operation, state: 'running'});
      }
    }
    if (url.pathname.startsWith('/v1/platform-tenant-self/')) {
      if (headers.get('authorization') !== 'Bearer customer-token') return Response.json({code: 'not_found'}, {status: 404});
      if (url.pathname.endsWith('/identity')) return Response.json({account_id: ids.account, platform_tenant_id: ids.tenant});
      if (url.pathname.endsWith('/submissions/lookup')) return Response.json(state.submissions.length ? {state: 'accepted', receipt,
        accepted_at: new Date().toISOString(), idempotency_expires_at: new Date(Date.now() + 86400000).toISOString()} : {state: 'unresolved'});
      if (url.pathname.endsWith('/customer-operations') && init.method === 'POST') {
        state.submissions.push({key: headers.get('Idempotency-Key'), body: JSON.parse(init.body)});
        if (state.lostSubmission && state.submissions.length === 1) throw new TypeError('acceptance acknowledgement lost');
        return Response.json(receipt);
      }
      if (url.pathname.endsWith('/customer-operations')) return Response.json({operations: state.submissions.length ? [snapshot()] : []});
      if (url.pathname.endsWith('/events')) return new Response(new ReadableStream({start(controller) {
        controller.enqueue(new TextEncoder().encode(': connected\n\n'));
        const close = () => { try { controller.close(); } catch { /* An aborted reader may already have cancelled its stream. */ } };
        if (init.signal.aborted) close(); else init.signal.addEventListener('abort', close, {once: true});
      }}), {headers: {'Content-Type': 'text/event-stream'}});
      if (url.pathname.endsWith('/' + ids.artifact)) return state.published ? new Response(state.retained) : Response.json({code: 'not_found'}, {status: 404});
      return Response.json(snapshot());
    }
    throw new Error('Unexpected fixture endpoint: ' + url.pathname);
  };
  const server = createExportServer({config: {...config, storageToken: 'must-not-leak'}});
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => { server.closeAllConnections(); server.close(); });
  const base = `http://127.0.0.1:${server.address().port}/`;
  return {state, fetchImpl, base, snapshot, confirm() { state.published = true; }};
}
