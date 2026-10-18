const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const token = 'A'.repeat(43), receipt = 'c31fb2b4-6010-5cff-8541-8dc45b785596';
function confirmation(fragment = '#token=' + token, respond = async () => ({ ok: true, status: 200, json: async () => ({ receipt_id: receipt, unsubscribed_at: new Date().toISOString(), current: true }) })) {
  const button = { disabled: true, textContent: 'Turn off my notifications' }, status = { textContent: '' }, calls = [], history = [], timers = [], events = {};
  const location = { href: 'https://portal.example/v1/product-control/email/unsubscribe' + fragment };
  const context = vm.createContext({ document: { getElementById: id => id === 'unsubscribe-confirm' ? button : status, body: { dataset: { requestTimeoutMs: '10000' } } }, location, history: { replaceState: (...args) => { history.push(args); location.href = 'https://portal.example' + args[2]; } }, URL, URLSearchParams, AbortController, addEventListener: (name, handler) => { events[name] = handler; },
    setTimeout: (task, ms) => { timers.push({ task, ms }); return timers.length; }, clearTimeout() {}, fetch: async (url, options) => { calls.push({ url, options }); return respond(url, options); } });
  vm.runInContext(fs.readFileSync(`${__dirname}/email-unsubscribe.js`, 'utf8'), context);
  return { button, status, calls, history, timers, navigate: fragment => { location.href = 'https://portal.example/v1/product-control/email/unsubscribe' + fragment; events.hashchange(); } };
}
test('opening an email confirmation strips the token without requests or preference changes', async () => {
  const b = confirmation();
  assert.equal(b.calls.length, 0); assert.equal(b.button.disabled, false);
  assert.deepEqual(b.history[0], [null, '', '/v1/product-control/email/unsubscribe']);
  await b.button.onclick();
  assert.equal(b.calls.length, 1); assert.equal(b.calls[0].url, '/v1/product-control/email/unsubscribe');
  assert.equal(b.calls[0].options.method, 'POST'); assert.equal(b.calls[0].options.credentials, 'omit');
  assert.equal(b.calls[0].options.redirect, 'error'); assert.equal(b.calls[0].options.cache, 'no-store');
  assert.deepEqual(JSON.parse(b.calls[0].options.body), { token });
  assert.match(b.status.textContent, /notifications are off.*already being sent/);
  await b.button.onclick(); assert.equal(b.calls.length, 1);
});
test('GET of a mailbox header URL still requires explicit human confirmation', async () => {
  const b = confirmation('?token=' + token); assert.equal(b.calls.length, 0);
  await b.button.onclick(); assert.equal(b.calls.length, 1);
  assert.equal(b.calls[0].url.includes('?'), false);
});
test('incomplete, duplicated and mixed capabilities are removed without requests', () => {
  for (const fragment of ['', '#token=', '#token=' + token + 'B', '#token=' + token.slice(0, 42) + 'B', '#token=' + token + '&token=' + token, '?token=' + token + '#token=' + token, '#token=' + token + '&tenant=other', '?token=' + token + '&token=' + token]) {
    const b = confirmation(fragment); assert.equal(b.button.disabled, true); assert.equal(b.calls.length, 0);
    assert.match(b.status.textContent, /incomplete/); assert.equal(b.history[0][2], '/v1/product-control/email/unsubscribe');
  }
});
test('lost responses retry only on a second click and preserve the exact capability', async () => {
  let count = 0;
  const b = confirmation('#token=' + token, async () => { if (++count === 1) throw new Error('lost'); return { ok: true, status: 200, json: async () => ({ receipt_id: receipt, unsubscribed_at: new Date().toISOString(), current: false }) }; });
  await b.button.onclick(); assert.equal(count, 1); assert.equal(b.button.disabled, false);
  assert.match(b.status.textContent, /may have completed/);
  await b.button.onclick(); assert.equal(count, 2);
  assert.equal(b.calls[0].options.body, b.calls[1].options.body);
  assert.match(b.status.textContent, /recorded previously.*Newer preferences.*left unchanged/);
});
test('expired links and rejected confirmations never show an unverified server message', async () => {
  for (const code of [400, 403, 410]) {
    const b = confirmation('#token=' + token, async () => ({ ok: false, status: code, json: async () => ({ detail: '<script>private payload</script>' }) }));
    await b.button.onclick(); assert.equal(b.button.disabled, true); assert.ok(!b.status.textContent.includes('private payload'));
    if (code === 410) assert.match(b.status.textContent, /newer preferences/);
    await b.button.onclick(); assert.equal(b.calls.length, 1);
  }
});
test('malformed acknowledgements stay uncertain and use the same confirmation on retry', async () => {
  for (const result of [{}, { receipt_id: receipt, unsubscribed_at: null, current: true }, { receipt_id: receipt, unsubscribed_at: new Date().toISOString(), current: 'true' }]) {
    const b = confirmation('#token=' + token, async () => ({ ok: true, status: 200, json: async () => result }));
    await b.button.onclick(); assert.equal(b.button.disabled, false); assert.match(b.status.textContent, /could not be checked/);
    await b.button.onclick(); assert.equal(b.calls[0].options.body, b.calls[1].options.body);
  }
});
test('a pending confirmation disables duplicate clicks and has a bounded request', async () => {
  let release;
  const b = confirmation('#token=' + token, () => new Promise(resolve => { release = resolve; }));
  const pending = b.button.onclick(); await b.button.onclick(); assert.equal(b.calls.length, 1);
  assert.equal(b.timers[0].ms, 10000); b.timers[0].task(); assert.equal(b.calls[0].options.signal.aborted, true);
  release({ ok: true, status: 200, json: async () => ({ receipt_id: receipt, unsubscribed_at: new Date().toISOString(), current: true }) }); await pending;
});

test('a new fragment in the same tab starts another explicit confirmation without posting', async () => {
  const b = confirmation(); await b.button.onclick(); assert.equal(b.button.disabled, true);
  const next = 'E'.repeat(43);
  b.navigate('#token=' + next); assert.equal(b.button.disabled, false); assert.equal(b.calls.length, 1);
  assert.equal(b.status.textContent, ''); assert.equal(b.history.at(-1)[2], '/v1/product-control/email/unsubscribe');
  await b.button.onclick(); assert.deepEqual(JSON.parse(b.calls[1].options.body), { token: next });
});

test('a delayed acknowledgement from an older link cannot replace a new confirmation', async () => {
  let release;
  const b = confirmation('#token=' + token, () => new Promise(resolve => { release = resolve; }));
  const pending = b.button.onclick(); b.navigate('#token=' + 'E'.repeat(43));
  release({ ok: true, status: 200, json: async () => ({ receipt_id: receipt, unsubscribed_at: new Date().toISOString(), current: true }) }); await pending;
  assert.equal(b.button.disabled, false); assert.equal(b.status.textContent, '');
  assert.equal(b.calls.length, 1);
  b.navigate('#token=incomplete'); assert.equal(b.button.disabled, true); assert.match(b.status.textContent, /incomplete/);
});
