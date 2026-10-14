// Heap checkpoints during an on-demand capture through the Node preload
// (ADR-1155): a process killed mid-window, like an OOM kill, must already
// have sent its recent heap. Run with NODE_PATH pointing at an install of
// guest/profiling/node: node tests/profiling/node_checkpoint_test.cjs
'use strict';
const assert = require('node:assert');
const {spawn} = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const zlib = require('node:zlib');

const APP = `
class Cache { constructor() { this.items = []; } grow() { this.items.push({label: "item-" + this.items.length, values: new Array(4096).fill(this.items.length)}); } }
const cache = new Cache();
function spin() { let x = 1; for (let i = 0; i < 2e5; i++) x = (x * 31 + i) % 1000003; return x; }
const end = Date.now() + 9000;
(function loop() { if (Date.now() > end) return; cache.grow(); spin(); setImmediate(loop); })();
`;

const control = {enabled: true, suspended: false, epoch: 'f'.repeat(32), window_seconds: 4, kinds: ['heap'], capture: true, checkpoint_seconds: 1};
const uploads = [];
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://bridge');
  if (req.method === 'GET' && url.pathname === '/control') {
    res.end(JSON.stringify(control));
    return;
  }
  const chunks = [];
  req.on('data', c => chunks.push(c));
  req.on('end', () => {
    uploads.push({query: url.searchParams, body: Buffer.concat(chunks)});
    res.statusCode = 204;
    res.end();
  });
});
const sleep = ms => new Promise(r => setTimeout(r, ms));

server.listen(0, '127.0.0.1', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'gregale-node-checkpoint-'));
  fs.writeFileSync(path.join(dir, 'app.js'), APP);
  // Without inlining the sampled stacks keep the app's own frames.
  const child = spawn(process.execPath, ['--no-turbo-inlining', '--no-maglev-inlining', '--require', path.resolve(__dirname, '../../guest/profiling/node.cjs'), path.join(dir, 'app.js')], {
    env: {...process.env, FAAS_PROFILING_ENABLED: '1', FAAS_PROFILING_ENDPOINT: 'http://127.0.0.1:' + server.address().port},
    stdio: 'inherit',
  });
  try {
    await sleep(3500);
    child.kill('SIGKILL');
    await sleep(200);
    const checkpoints = uploads.filter(u => u.query.get('checkpoint') === '1');
    assert.ok(checkpoints.length >= 2, 'expected about one checkpoint per second, got ' + checkpoints.length);
    assert.strictEqual(checkpoints.length, uploads.length, 'a killed process must not have sent a final heap');
    const last = checkpoints[checkpoints.length - 1];
    assert.strictEqual(last.query.get('kind'), 'heap');
    assert.ok(zlib.gunzipSync(last.body).includes('grow'), 'checkpoint lacks the allocating function');
    console.log('node checkpoint: ok');
  } finally {
    child.kill();
    server.close();
  }
});
