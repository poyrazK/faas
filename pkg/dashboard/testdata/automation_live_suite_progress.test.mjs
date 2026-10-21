import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import test from 'node:test';

const template = readFileSync(new URL('../templates/automation_live_suite_progress.html', import.meta.url), 'utf8');
const source = template.match(/<script[^>]*>([\s\S]*?)<\/script>/)[1];

// Exercise the shipped script with controllable network, visibility and timers.
// HTML escaping and server-rendered target attributes have separate Go tests.
function browser({ auto = false, limit = 100, visible = true, fetcher } = {}) {
  const events = {}, formEvents = {}, windowEvents = {}, timers = new Map(), calls = [];
  const status = { textContent: '' }, pause = { hidden: true, addEventListener(type, fn) { events[`pause:${type}`] = fn; } };
  const confirmation = { checked: false };
  const form = { querySelector: () => ({ value: 'csrf-value' }), addEventListener(type, fn) { formEvents[type] = fn; } };
  const target = { suiteId: 'original-suite', suiteManaged: 'true', suiteVersion: '7', suitePublished: '4', suiteDeployment: 'original-deployment' };
  let result;
  function node(dataset) { return { dataset: { ...dataset }, closest: () => form, querySelector: () => confirmation, replaceWith(next) { result = next; } }; }
  result = node({ ...target, suiteOutcome: 'pending' });
  const panel = { dataset: { url: '/dashboard/apps/demo/automations/live-suites/original-suite/advance?name=report', auto: String(auto), intervalMs: '3000', timeoutMs: '10000', maxRequests: String(limit) }, querySelector: selector => selector.includes('status') ? status : pause };
  const document = { visibilityState: visible ? 'visible' : 'hidden', getElementById: id => id.endsWith('progress') ? panel : result, importNode: next => next, addEventListener(type, fn) { events[type] = fn; } };
  let timerID = 0;
  const context = vm.createContext({ document, location: { href: 'https://gregale.test/dashboard/apps/demo/automations', origin: 'https://gregale.test' }, window: { addEventListener(type, fn) { windowEvents[type] = fn; } }, URL, URLSearchParams, AbortController,
    setTimeout(fn, delay) { const id = ++timerID; timers.set(id, { fn, delay }); return id; }, clearTimeout(id) { timers.delete(id); },
    fetch: async (url, options) => { calls.push({ url, ...options }); return fetcher ? await fetcher(calls.length, options) : response('pending'); },
    DOMParser: class { parseFromString(raw) { const data = JSON.parse(raw); return { querySelectorAll: () => [node({ ...target, ...data })] }; } },
  });
  vm.runInContext(source, context);
  function response(outcome, overrides = {}, statusCode = 200) { return { ok: statusCode === 200, status: statusCode, headers: { get: () => 'text/html; charset=utf-8' }, text: async () => JSON.stringify({ suiteOutcome: outcome, ...overrides }) }; }
  return { calls, status, pause, confirmation, timers, response, result: () => result,
    confirm() { confirmation.checked = true; formEvents.submit({ submitter: { matches: () => true }, preventDefault() {} }); },
    submitWithoutConfirmation() { confirmation.checked = false; formEvents.submit({ submitter: { matches: () => true }, preventDefault() {} }); },
    hide() { document.visibilityState = 'hidden'; events.visibilitychange(); },
    show() { document.visibilityState = 'visible'; events.visibilitychange(); },
    leave() { windowEvents.pagehide(); }, stop() { events['pause:click'](); },
    async tick() { const first = [...timers].sort((a,b) => a[1].delay-b[1].delay)[0]; if (!first) return false; timers.delete(first[0]); await first[1].fn(); return true; },
  };
}

test('history browsing stays read-only until confirmation; progress sends only a narrow POST', async () => {
  let env;
  env = browser({ fetcher: async n => env.response(n === 1 ? 'pending' : 'passed') });
  assert.equal(env.timers.size, 0); assert.equal(env.calls.length, 0);
  env.submitWithoutConfirmation(); assert.equal(env.timers.size, 0);
  assert.match(env.status.textContent, /Confirm real actions/);
  env.confirm(); await env.tick();
  assert.equal(env.calls.length, 1);
  const call = env.calls[0];
  assert.equal(call.method, 'POST'); assert.equal(call.credentials, 'same-origin'); assert.equal(call.redirect, 'error');
  assert.equal(call.body.toString(), 'csrf_token=csrf-value&confirm_real_actions=yes');
  assert.match(call.url, /original-suite\/advance\?name=report$/);
  await env.tick(); assert.equal(env.result().dataset.suiteOutcome, 'passed');
  assert.equal(env.timers.size, 0); assert.equal(env.pause.hidden, true);
});

test('a confirmed start advances automatically; stop and reopen preserve explicit continuation', async () => {
  const env = browser({ auto: true });
  await env.tick(); assert.equal(env.calls.length, 1);
  env.stop(); assert.equal(env.timers.size, 0);
  env.confirm(); await env.tick(); assert.equal(env.calls.length, 2);
  assert.equal(env.calls[0].url, env.calls[1].url);
  env.leave(); assert.equal(env.timers.size, 0);
  const reopened = browser(); assert.equal(reopened.timers.size, 0);
});

test('hiding during a request stops advancement and ignores its late response', async () => {
  let complete;
  const pending = new Promise(resolve => { complete = resolve; });
  const env = browser({ auto: true, fetcher: () => pending });
  const inFlight = env.tick(); assert.equal(env.calls.length, 1);
  env.hide(); assert.equal(env.calls[0].signal.aborted, true);
  complete(env.response('passed')); await inFlight;
  assert.equal(env.result().dataset.suiteOutcome, 'pending'); assert.equal(env.timers.size, 0);
  env.show(); assert.equal(env.timers.size, 0);
  const hidden = browser({ auto: true, visible: false }); hidden.show(); assert.equal(hidden.timers.size, 0);
});

test('wrong target or unresolved outcomes stop further requests', async () => {
  for (const overrides of [{ suiteId: 'foreign-suite' }, { suiteDeployment: 'foreign-deployment' }, { suiteOutcome: 'unknown' }]) {
    let env; env = browser({ auto: true, fetcher: () => env.response('pending', overrides) });
    await env.tick(); assert.equal(env.calls.length, 1); assert.equal(env.timers.size, 0);
    assert.equal(env.result().dataset.suiteId, 'original-suite');
    assert.match(env.status.textContent, /identity changed/);
  }
  for (const outcome of ['failed', 'outdated', 'unavailable']) {
    let env; env = browser({ auto: true, fetcher: () => env.response(outcome) });
    await env.tick(); assert.equal(env.calls.length, 1); assert.equal(env.timers.size, 0);
    assert.match(env.status.textContent, new RegExp(outcome));
  }
});

test('lost acknowledgements and permission failures require explicit continuation of the same suite', async () => {
  for (const statusCode of [403, 503]) {
    let env; env = browser({ auto: true, fetcher: n => env.response('pending', {}, n === 1 ? statusCode : 200) });
    await env.tick(); assert.equal(env.timers.size, 0);
    env.confirm(); await env.tick(); assert.equal(env.calls.length, 2);
    assert.equal(env.calls[0].url, env.calls[1].url); env.stop();
  }
});

test('automatic requests stay bounded and repeated confirmation does not overlap requests', async () => {
  const bounded = browser({ auto: true, limit: 2 });
  await bounded.tick(); await bounded.tick(); await bounded.tick();
  assert.equal(bounded.calls.length, 2); assert.equal(bounded.timers.size, 0);
  let complete; const pending = new Promise(resolve => { complete = resolve; });
  const env = browser({ auto: true, fetcher: () => pending });
  const first = env.tick(); env.confirm(); await env.tick(); assert.equal(env.calls.length, 1);
  complete(env.response('pending')); await first; env.stop();
});
