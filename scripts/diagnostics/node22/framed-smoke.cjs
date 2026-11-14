const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const readline = require('node:readline');
const path = require('node:path');

async function checkWorker(binary, fixture, number) {
  const child = spawn(path.resolve(binary), [path.resolve(fixture, 'node22.js')], {
    env: { ...process.env, FAAS_PERSISTENT_WORKER: '1' },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let errors = '';
  child.stderr.on('data', data => {
    // The fixture logs only synthetic invocation IDs. Bound retained errors.
    errors = (errors + data.toString()).slice(-4096);
  });
  const lines = readline.createInterface({ input: child.stdout });
  const iterator = lines[Symbol.asyncIterator]();
  const exited = new Promise(resolve => child.once('close', (code, signal) => resolve({ code, signal })));
  const timer = setTimeout(() => child.kill('SIGTERM'), 60000);
  try {
    const ready = await iterator.next();
    assert.equal(ready.done, false);
    assert.equal(JSON.parse(ready.value).__faas_ready, true);
    for (let i = 0; i < 2000; i++) {
      const id = `diagnostic-${number}-${i}`;
      child.stdin.write(JSON.stringify({ method: 'GET', path: '/', body_b64: '',
        headers: { 'x-faas-invocation-id': id } }) + '\n');
      const response = await iterator.next();
      assert.equal(response.done, false, errors);
      const envelope = JSON.parse(response.value);
      assert.equal(envelope.status, 200, errors);
      const body = JSON.parse(Buffer.from(envelope.body_b64, 'base64').toString('utf8'));
      assert.equal(body.ok, true);
      assert.equal(body.invocation_id, id);
    }
    child.stdin.end();
    const exit = await exited;
    assert.deepEqual(exit, { code: 0, signal: null }, errors);
    return 2000;
  } finally {
    clearTimeout(timer);
    lines.close();
    if (child.exitCode === null && child.signalCode === null) child.kill('SIGTERM');
  }
}

(async () => {
  const [binary, fixture] = process.argv.slice(2);
  assert(binary && fixture, 'binary and fixture paths required');
  const counts = await Promise.all([0, 1, 2, 3].map(n => checkWorker(binary, fixture, n)));
  console.log(JSON.stringify({ workers: 4, correct_responses: counts.reduce((a, b) => a + b, 0) }));
})().catch(error => { console.error(error); process.exitCode = 1; });
