// The Node preload reports how guest-init may reach its inspector for stack
// snapshots of a stuck main thread (ADR-1155). Run with NODE_PATH pointing at
// an install of guest/profiling/node:
//   node tests/profiling/node_inspector_state_test.cjs
'use strict';
const assert = require('node:assert');
const {spawn} = require('node:child_process');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');

const PLAIN = `setTimeout(() => process.exit(0), 6000);`;
const OWNS_SIGUSR1 = `process.on('SIGUSR1', () => {}); setTimeout(() => process.exit(0), 6000);`;
const OPEN = `require('node:inspector').open(0, '127.0.0.1'); setTimeout(() => process.exit(0), 6000);`;

const polls = new Map();
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://bridge');
  if (url.pathname === '/control') {
    polls.set(url.searchParams.get('pid'), url.searchParams.get('inspector'));
    res.end(JSON.stringify({enabled: false, suspended: false, epoch: 'c'.repeat(32), window_seconds: 2}));
    return;
  }
  res.statusCode = 404;
  res.end();
});
const sleep = ms => new Promise(r => setTimeout(r, ms));

server.listen(0, '127.0.0.1', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'gregale-node-inspector-'));
  const start = (name, source, extraArgs = []) => {
    fs.writeFileSync(path.join(dir, name), source);
    return spawn(process.execPath, [...extraArgs, '--require', path.resolve(__dirname, '../../guest/profiling/node.cjs'), path.join(dir, name)], {
      env: {...process.env, FAAS_PROFILING_ENABLED: '1', FAAS_PROFILING_ENDPOINT: 'http://127.0.0.1:' + server.address().port},
      stdio: 'inherit',
    });
  };
  const plain = start('plain.js', PLAIN);
  const owner = start('owner.js', OWNS_SIGUSR1);
  const ported = start('ported.js', PLAIN, ['--inspect-port=9330']);
  const open = start('open.js', OPEN);
  try {
    for (let i = 0; i < 40 && polls.size < 4; i++) await sleep(100);
    await sleep(1200); // the open-inspector app reports on its second poll at the latest
    assert.strictEqual(polls.get(String(plain.pid)), 'sigusr1:9229', 'plain app must report SIGUSR1 on the default port');
    assert.strictEqual(polls.get(String(owner.pid)), null, 'an app listening for SIGUSR1 must not be signalled');
    assert.strictEqual(polls.get(String(ported.pid)), 'sigusr1:9330', '--inspect-port must be reported');
    assert.match(polls.get(String(open.pid)) || '', /^open:\d+$/, 'an open inspector must be reported with its port');
    const hook = Symbol.for('gregale.profiling');
    assert.strictEqual(typeof globalThis[hook], 'undefined', 'the test process itself has no preload');
    console.log('node inspector state: ok');
  } finally {
    for (const child of [plain, owner, ported, open]) child.kill();
    server.close();
  }
});
