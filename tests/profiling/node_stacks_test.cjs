// Stack snapshots through the Node preload (ADR-1155): Node reports what the
// event loop is waiting on, by handle type. Run with NODE_PATH pointing at an
// install of guest/profiling/node: node tests/profiling/node_stacks_test.cjs
'use strict';
const assert = require('node:assert');
const {spawn} = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const zlib = require('node:zlib');

const APP = `
require('node:http').createServer(() => {}).listen(0);
setTimeout(() => {}, 60000);
setTimeout(() => process.exit(0), 9000);
`;

let control = {enabled: false, suspended: false, epoch: 'b'.repeat(32), window_seconds: 2};
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
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'gregale-node-stacks-'));
  fs.writeFileSync(path.join(dir, 'app.js'), APP);
  const child = spawn(process.execPath, ['--require', path.resolve(__dirname, '../../guest/profiling/node.cjs'), path.join(dir, 'app.js')], {
    env: {...process.env, FAAS_PROFILING_ENABLED: '1', FAAS_PROFILING_ENDPOINT: 'http://127.0.0.1:' + server.address().port},
    stdio: 'inherit',
  });
  try {
    await sleep(1500);
    control = {enabled: true, suspended: false, epoch: 'a'.repeat(32), window_seconds: 2, kinds: ['stacks'], capture: true};
    for (let i = 0; i < 50 && uploads.length === 0; i++) await sleep(100);
    await sleep(1000); // a second snapshot would arrive within a poll
    assert.strictEqual(uploads.length, 1, 'expected one snapshot per capture epoch, got ' + uploads.length);
    assert.strictEqual(uploads[0].query.get('kind'), 'stacks');
    const raw = zlib.gunzipSync(uploads[0].body).toString('latin1');
    for (const want of ['threads', '<event loop>', 'waiting on TCPServerWrap', 'waiting on Timeout']) {
      assert.ok(raw.includes(want), 'stacks snapshot lacks ' + want);
    }
    console.log('node stacks: ok');
  } finally {
    child.kill();
    server.close();
  }
});
