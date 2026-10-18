import assert from 'node:assert/strict'
import http from 'node:http'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createRequire } from 'node:module'

export async function mutationJournalBrowserOrigin(output) {
  const require = createRequire(join(process.env.DATA_API_STARTER_DIR, 'client/package.json'))
  const { build } = require('esbuild')
  const bundle = await build({ stdin: { contents: "export * from './dist/client.js'; export {createIndexedDBMutationStore} from '@gregale/data'", resolveDir: output }, bundle: true, format: 'esm', platform: 'browser', target: 'es2022', write: false })
  const server = http.createServer((req, res) => {
    if (!['/', '/client.js'].includes(req.url)) { res.writeHead(404); res.end(); return }
    res.writeHead(200, { 'Content-Type': req.url === '/' ? 'text/html' : 'text/javascript', 'Cache-Control': 'no-store' })
    res.end(req.url === '/' ? '<!doctype html><title>Mutation journal acceptance</title>' : bundle.outputFiles[0].contents)
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  return { url: `http://127.0.0.1:${server.address().port}`, async close() { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) } }
}

export async function verifyMutationJournalBrowser({ origin, url, token, expiredKey, owner }) {
  const profile = await mkdtemp(join(tmpdir(), 'gregale-journal-browser-'))
  let spawnError
  const launch = () => {
    const child = spawn(process.env.DATA_API_CHROMIUM_BIN, ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-first-run', '--disable-background-networking', '--disk-cache-size=1', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { detached: true, stdio: ['ignore', 'ignore', 'pipe'] })
    child.on('error', error => { spawnError = error }); child.stderr.on('data', () => {})
    return child
  }
  let child = launch(), closed = new Promise(resolve => child.once('close', resolve))
  const sockets = [], pending = new Set()

  try {
    let port
    for (const deadline = Date.now() + 30000; Date.now() < deadline;) {
      if (spawnError || child.exitCode !== null) break
      try { const value = await readFile(join(profile, 'DevToolsActivePort'), 'utf8'); port = /^([1-9]\d*)\r?\n/.exec(value)?.[1]; if (port) break } catch {}
      await new Promise(resolve => setTimeout(resolve, 30))
    }
    assert.ok(port && !spawnError, 'journal_chromium_startup_failed')
    const targets = async () => (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
    const channel = async page => {
      const socket = new WebSocket(page.webSocketDebuggerUrl); sockets.push(socket)
      await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }) })
      let serial = 0
      const commands = new Map()
      socket.addEventListener('message', event => {
        const result = JSON.parse(event.data), request = commands.get(result.id)
        if (request) { commands.delete(result.id); pending.delete(request); clearTimeout(request.timer); result.error ? request.reject(new Error('journal_browser_protocol_failed')) : request.resolve(result.result) }
      })
      const send = (method, params = {}) => new Promise((resolve, reject) => {
        const id = ++serial, request = { resolve, reject, timer: setTimeout(() => { commands.delete(id); pending.delete(request); reject(new Error('journal_browser_command_timeout')) }, 15000) }
        pending.add(request); commands.set(id, request); socket.send(JSON.stringify({ id, method, params }))
      })
      const evaluate = async expression => {
        const result = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
        assert.equal(result.exceptionDetails, undefined, 'journal_browser_evaluation_failed')
        return result.result.value
      }
      const navigate = async () => {
        await send('Page.navigate', { url: origin.url })
        for (const deadline = Date.now() + 15000; Date.now() < deadline;) {
          try { if (await evaluate(`location.origin===${JSON.stringify(origin.url)} && document.readyState==='complete'`)) return } catch {}
          await new Promise(resolve => setTimeout(resolve, 30))
        }
        assert.fail('journal_browser_navigation_timeout')
      }
      return { send, evaluate, navigate }
    }
    const a = await channel((await targets()).find(page => page.type === 'page'))
    await a.navigate()
    const created = await a.send('Target.createTarget', { url: 'about:blank' })
    const b = await channel((await targets()).find(page => page.id === created.targetId)); await b.navigate()
    const setup = async (page, drop = false) => page.evaluate(`(async()=>{
      const module=await import('/client.js'); globalThis.module=module; globalThis.calls=[];
      globalThis.options={url:${JSON.stringify(url)},subject:'alice',tenant:'alpha',accessToken:${JSON.stringify(token)},fetch:async(...args)=>{
        const path=new URL(String(args[0])).pathname; calls.push(path); const response=await fetch(...args);
        if(${drop} && path.endsWith('/rpc/place_order_v2')){await response.arrayBuffer();throw new TypeError('lost after commit')}
        return response;
      }};
      globalThis.store=module.createIndexedDBMutationStore({databaseName:'transaction-journal'});
      globalThis.transaction=module.persistentTransactionClient(options,store);
      return true;
    })()`)
    await setup(a, true); await setup(b, true)
    const staged = await a.evaluate("(async()=>{globalThis.entry=await transaction.prepare('checkout',{skus:['a_widget'],quantities:[2]});return {key:entry.operation.key,state:entry.state,hasToken:(await store.get('checkout')).includes(options.accessToken)}})()")
    assert.equal(staged.state, 'prepared'); assert.equal(staged.hasToken, false)
    assert.equal(await b.evaluate("(async()=>{globalThis.entry=await transaction.load('checkout');return entry.operation.key})()"), staged.key)
    const send = page => page.evaluate("(async()=>{try{const result=await transaction.execute(entry,{timeoutMs:5000});globalThis.entry=result.entry;return {status:result.response.status}}catch(error){return {code:error.code}}})()")
    const raced = await Promise.all([send(a), send(b)])
    assert.equal(raced.filter(result => result.status === 0).length, 1); assert.equal(raced.filter(result => result.code === 'conflict').length, 1)
    const calls = await Promise.all([a.evaluate('calls.length'), b.evaluate('calls.length')]); assert.equal(calls[0] + calls[1], 1)
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.place_order_v2_receipts WHERE request_key=$1', [staged.key])).rows[0].n, 1)

    // Reload destroys the JS client and handle maps. Only IndexedDB retains
    // the original identity, key, canonical payload and uncertain dispatch.
    await a.navigate(); assert.equal(await a.evaluate("typeof globalThis.entry==='undefined'"), true); await setup(a)
    const loaded = await a.evaluate("(async()=>{globalThis.entry=await transaction.load('checkout');return {key:entry.operation.key,state:entry.state,quantities:entry.operation.quantities,calls:calls.length,frozen:Object.isFrozen(entry.operation.skus)}})()")
    assert.deepEqual(loaded, { key: staged.key, state: 'uncertain', quantities: [2], calls: 0, frozen: true })
    const recovered = await a.evaluate("(async()=>{const recovered=await transaction.recover(entry,{timeoutMs:5000});globalThis.entry=recovered.entry;return {outcome:recovered.result.outcome,persistence:recovered.persistence,state:entry.state,total:recovered.result.row?.total_cents,paths:calls}})()")
    assert.deepEqual(recovered, { outcome: 'confirmed', persistence: 'saved', state: 'confirmed', total: 1000, paths: ['/rest/v1/rpc/read_place_order_receipt_v2'] })
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.place_order_v2_orders WHERE request_key=$1', [staged.key])).rows[0].n, 1)

    // Both tabs can observe confirmation, but a stale confirmed handle cannot
    // delete a new operation staged into the same application slot.
    assert.equal(await b.evaluate("(async()=>{globalThis.entry=await transaction.load('checkout');return entry.state})()"), 'confirmed')
    const nextKey = await a.evaluate("(async()=>{await transaction.removeConfirmed(entry);globalThis.entry=await transaction.prepare('checkout',{skus:['a_widget'],quantities:[1]});return entry.operation.key})()")
    assert.notEqual(nextKey, staged.key)
    assert.equal(await b.evaluate("(async()=>{try{await transaction.removeConfirmed(entry)}catch(error){return error.code}})()"), 'conflict')
    assert.equal(await b.evaluate("(async()=>{globalThis.entry=await transaction.load('checkout');return entry.operation.key})()"), nextKey)
    assert.deepEqual(await b.evaluate("(async()=>{const before=calls.length;store.close();try{await transaction.execute(entry,{timeoutMs:5000})}catch(error){return {code:error.code,sent:calls.length-before}}})()"), { code: 'storage_unavailable', sent: 0 })
    assert.equal(await a.evaluate("(async()=>{return (await transaction.load('checkout')).state})()"), 'prepared')

    const mismatch = await a.evaluate(`(async()=>{
      const before=calls.length,codes=[];
      for(const changed of [{subject:'bob'},{tenant:'beta'},{url:'https://other.example'},{expectedSchemaFingerprint:'a'.repeat(64)}]){
        try{await module.persistentTransactionClient({...options,...changed},store).load('checkout')}catch(error){codes.push(error.code)}
      }
      return {codes,sent:calls.length-before};
    })()`)
    assert.deepEqual(mismatch, { codes: Array(4).fill('context_mismatch'), sent: 0 })
    assert.deepEqual(await a.evaluate(`(async()=>{
      const limited=module.createIndexedDBMutationStore({databaseName:'quota-journal',maxRecords:1}), client=module.persistentTransactionClient(options,limited);
      await client.prepare('one',{skus:['a_widget'],quantities:[1]});let code;
      try{await client.prepare('two',{skus:['a_widget'],quantities:[1]})}catch(error){code=error.code}
      const remaining=(await client.load('one')).state,absent=await client.load('two');limited.close();return {code,remaining,absent};
    })()`), { code: 'storage_full', remaining: 'prepared', absent: null })
    assert.equal(await a.evaluate(`(async()=>{
      const limited=module.createIndexedDBMutationStore({databaseName:'bytes-journal',maxBytes:1});try{await module.persistentTransactionClient(options,limited).prepare('one',{skus:['a_widget'],quantities:[1]})}catch(error){limited.close();return error.code}
    })()`), 'storage_full')
    assert.deepEqual(await a.evaluate(`(async()=>{
      await new Promise((resolve,reject)=>{const request=indexedDB.open('transaction-journal',1);request.onsuccess=()=>{const db=request.result,tx=db.transaction('entries','readwrite');tx.objectStore('entries').put('{broken','corrupt');tx.oncomplete=()=>{db.close();resolve()};tx.onabort=reject};request.onerror=reject});
      let code;try{await transaction.load('corrupt')}catch(error){code=error.code}return {code,raw:await store.get('corrupt')};
    })()`), { code: 'invalid_record', raw: '{broken' })
    assert.deepEqual(await a.evaluate(`(async()=>{
      const isolated=module.createIndexedDBMutationStore({databaseName:'null-corrupt-journal'}),client=module.persistentTransactionClient(options,isolated);await client.load('missing');
      await new Promise((resolve,reject)=>{const request=indexedDB.open('null-corrupt-journal',1);request.onsuccess=()=>{const db=request.result,tx=db.transaction('entries','readwrite');tx.objectStore('entries').put(null,'null-corrupt');tx.oncomplete=()=>{db.close();resolve()};tx.onabort=reject};request.onerror=reject});
      let code;try{await client.load('null-corrupt')}catch(error){code=error.code}isolated.close();return {code};
    })()`), { code: 'invalid_record' })
    assert.equal(await a.evaluate(`(async()=>{
      const original=IDBDatabase.prototype.transaction;
      IDBDatabase.prototype.transaction=function(names,mode){return original.call(this,names,mode)};
      const unsupported=module.createIndexedDBMutationStore({databaseName:'relaxed-journal'});
      try{await module.persistentTransactionClient(options,unsupported).prepare('one',{skus:['a_widget'],quantities:[1]})}catch(error){return error.code}
      finally{IDBDatabase.prototype.transaction=original;unsupported.close()}
    })()`), 'storage_unavailable')
    const expired = await a.evaluate(`(async()=>{
      const pending=await transaction.prepare('expired',{skus:['a_widget'],quantities:[1]},${JSON.stringify(expiredKey)});
      const response=await transaction.execute(pending,{timeoutMs:5000});const checked=await transaction.reconcile(response.entry,response.response);
      const restored=await transaction.load('expired');const recovered=await transaction.recover(restored,{timeoutMs:5000});
      return {reason:checked.result.reason,persistence:checked.persistence,state:restored.state,key:restored.operation.key,recovered:recovered.result.reason};
    })()`)
    assert.deepEqual(expired, { reason: 'replay_expired', persistence: 'unchanged', state: 'uncertain', key: expiredKey, recovered: 'replay_expired' })

    const crash = await a.evaluate(`(async()=>{
      const lossy=module.persistentTransactionClient({...options,fetch:async(...args)=>{const response=await fetch(...args);await response.arrayBuffer();throw new TypeError('lost before process crash')}},store);
      const entry=await lossy.prepare('crash-checkout',{skus:['a_widget'],quantities:[1]});const sent=await lossy.execute(entry,{timeoutMs:5000});return {key:entry.operation.key,status:sent.response.status};
    })()`)
    assert.equal(crash.status, 0)
    process.kill(-child.pid, 'SIGKILL'); await closed
    await rm(join(profile, 'DevToolsActivePort'), { force: true })
    spawnError = undefined; child = launch(); closed = new Promise(resolve => child.once('close', resolve)); port = undefined
    for (const deadline = Date.now() + 30000; Date.now() < deadline;) {
      if (spawnError || child.exitCode !== null) break
      try { port = /^([1-9]\d*)\r?\n/.exec(await readFile(join(profile, 'DevToolsActivePort'), 'utf8'))?.[1]; if (port) break } catch {}
      await new Promise(resolve => setTimeout(resolve, 30))
    }
    assert.ok(port && !spawnError, 'journal_chromium_restart_failed')
    const restarted = await channel((await targets()).find(page => page.type === 'page')); await restarted.navigate(); await setup(restarted)
    const afterCrash = await restarted.evaluate(`(async()=>{globalThis.entry=await transaction.load('crash-checkout');return {key:entry.operation.key,state:entry.state,calls:calls.length}})()`)
    assert.deepEqual(afterCrash, { key: crash.key, state: 'uncertain', calls: 0 })
    const crashRecovery = await restarted.evaluate(`(async()=>{const result=await transaction.recover(entry,{timeoutMs:5000});return {outcome:result.result.outcome,persistence:result.persistence,paths:calls}})()`)
    assert.deepEqual(crashRecovery, { outcome: 'confirmed', persistence: 'saved', paths: ['/rest/v1/rpc/read_place_order_receipt_v2'] })
    assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.place_order_v2_orders WHERE request_key=$1', [crash.key])).rows[0].n, 1)

  } finally {
    for (const request of pending) { clearTimeout(request.timer); request.reject(new Error('journal_browser_closed')) }
    for (const socket of sockets) socket.close()
    if (child.exitCode === null) {
      try { process.kill(-child.pid, 'SIGTERM') } catch {}
      await Promise.race([closed, new Promise(resolve => setTimeout(resolve, 5000))])
      if (child.exitCode === null) { try { process.kill(-child.pid, 'SIGKILL') } catch {}; await closed }
    }
    await rm(profile, { recursive: true, force: true })
  }
}
