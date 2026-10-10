// On-demand CPU and heap capture through the Node preload against a fake
// bridge (ADR-967). Run with NODE_PATH pointing at an install of
// guest/profiling/node: node tests/profiling/node_capture_test.cjs [out-dir]
'use strict';
const assert = require('node:assert');
const {spawn} = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const zlib = require('node:zlib');

const EPOCH = 'e'.repeat(32);
const APP = `
class Cache { constructor() { this.items = []; } grow() { this.items.push({label: "item-" + this.items.length, values: new Array(256).fill(this.items.length)}); } }
const cache = new Cache();
function spin() { let x = 1; for (let i = 0; i < 2e5; i++) x = (x * 31 + i) % 1000003; return x; }
const end = Date.now() + 9000;
(function loop() { if (Date.now() > end) return; cache.grow(); spin(); setImmediate(loop); })();
`;

let control = {enabled: false, suspended: false, epoch: 'f'.repeat(32), window_seconds: 2};
const polls = [];
const uploads = [];
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://bridge');
  if (req.method === 'GET' && url.pathname === '/control') {
    polls.push(Date.now());
    res.end(JSON.stringify(control));
    return;
  }
  const chunks = [];
  req.on('data', c => chunks.push(c));
  req.on('end', () => {
    uploads.push({path: url.pathname, query: url.searchParams, body: Buffer.concat(chunks)});
    res.statusCode = 204;
    res.end();
  });
});

const sleep = ms => new Promise(r => setTimeout(r, ms));

server.listen(0, '127.0.0.1', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'gregale-node-capture-'));
  fs.writeFileSync(path.join(dir, 'app.js'), APP);
  const child = spawn(process.execPath, ['--require', path.resolve(__dirname, '../../guest/profiling/node.cjs'), path.join(dir, 'app.js')], {
    env: {...process.env, FAAS_PROFILING_ENABLED: '1', FAAS_PROFILING_ENDPOINT: 'http://127.0.0.1:' + server.address().port},
    stdio: 'inherit',
  });
  try {
    await sleep(2500);
    assert.ok(polls.length >= 1 && polls.length <= 4, 'dormant collector polled ' + polls.length + ' times in 2.5s');
    control = {enabled: true, suspended: false, epoch: EPOCH, window_seconds: 2, kinds: ['cpu', 'heap'], capture: true};
    await sleep(2000);
    control = {...control, enabled: false};
    for (let i = 0; i < 40 && uploads.length < 2; i++) await sleep(100);
    const byKind = Object.fromEntries(uploads.map(u => [u.query.get('kind'), u]));
    assert.strictEqual(uploads.length, 2, 'expected one CPU and one heap upload, got ' + uploads.length);
    for (const kind of ['cpu', 'heap']) {
      const u = byKind[kind];
      assert.ok(u, kind + ' upload missing');
      assert.ok(u.query.get('name').includes('gregale_epoch=' + EPOCH), kind + ' upload lost the capture epoch');
      const raw = zlib.gunzipSync(u.body);
      if (process.argv[2]) fs.writeFileSync(path.join(process.argv[2], kind + '.pb.gz'), u.body);
      assert.ok(raw.includes(kind === 'cpu' ? 'spin' : 'grow'), kind + ' profile lacks the hot function');
    }
    console.log('node capture: ok');
  } finally {
    child.kill();
    server.close();
  }
});
