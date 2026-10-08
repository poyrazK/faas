import assert from 'node:assert/strict'
import http from 'node:http'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

export async function browserOrigins(root) {
  const serve = async () => {
    const server = http.createServer(async (req, res) => {
      const file = req.url === '/' ? 'index.html' : req.url === '/client.js' ? 'client.js' : null
      if (!file) { res.writeHead(404); res.end(); return }
      res.writeHead(200, { 'Content-Type': file.endsWith('.js') ? 'text/javascript' : 'text/html', 'Cache-Control': 'no-store' })
      res.end(await readFile(join(root, 'client/browser', file)))
    })
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
    return { server, url: `http://127.0.0.1:${server.address().port}` }
  }
  const allowed = await serve(), denied = await serve()
  return { allowed: allowed.url, denied: denied.url, close: async () => { for (const item of [allowed, denied]) { item.server.closeAllConnections(); await new Promise(resolve => item.server.close(resolve)) } } }
}

export async function verifyBrowserCORS({ origins, url, token, expired, subject, gateway, expectedID }) {
  const profile = await mkdtemp(join(tmpdir(), 'gregale-cors-chromium-'))
  const requests = []
  const capture = req => requests.push({ path: req.url, method: req.method, origin: req.headers.origin, cookie: req.headers.cookie, authorization: Boolean(req.headers.authorization) })
  gateway.on('request', capture)
  const child = spawn(process.env.DATA_API_CHROMIUM_BIN, ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-first-run', '--disable-background-networking', '--disk-cache-size=1', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' })
  let socket, spawnError
  const pending = new Map()
  child.on('error', error => { spawnError = error })
  try {
    let port
    for (let i = 0; i < 100; i++) {
      try { port = (await readFile(join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; break } catch {}
      await new Promise(resolve => setTimeout(resolve, 50))
    }
    assert.equal(spawnError, undefined, 'chromium_failed_to_start')
    assert.ok(port, 'chromium_not_ready')
    const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
    const page = targets.find(target => target.type === 'page')
    socket = new WebSocket(page.webSocketDebuggerUrl)
    await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }) })
    let serial = 0
    socket.addEventListener('message', event => {
      const data = JSON.parse(event.data)
      if (pending.has(data.id)) { const { resolve, reject, timer } = pending.get(data.id); clearTimeout(timer); pending.delete(data.id); data.error ? reject(new Error('browser_protocol_failed')) : resolve(data.result) }
    })
    const send = (method, params = {}) => new Promise((resolve, reject) => {
      const id = ++serial
      const timer = setTimeout(() => { pending.delete(id); reject(new Error('browser_command_timeout')) }, 10000)
      pending.set(id, { resolve, reject, timer }); socket.send(JSON.stringify({ id, method, params }))
    })
    const evaluate = async expression => {
      const data = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })
      assert.equal(data.exceptionDetails, undefined, 'browser_evaluation_failed')
      return data.result.value
    }
    const navigate = async target => {
      await send('Page.navigate', { url: target })
      for (let i = 0; i < 100; i++) {
        if (await evaluate(`location.origin === ${JSON.stringify(target)} && document.readyState === 'complete'`)) return
        await new Promise(resolve => setTimeout(resolve, 20))
      }
      throw new Error('browser_navigation_timeout')
    }
    await navigate(origins.allowed)
    await evaluate("document.cookie='browser_cookie_sentinel=private; SameSite=Strict; path=/'")
    const input = JSON.stringify({ url, subject, token, expired })
    const allowed = await evaluate(`(async () => {
      const input = ${input}; const { readBrowserNotes } = await import('/client.js');
      const diagnostics = [];
      const result = await readBrowserNotes({ url: input.url, subject: input.subject, accessToken: () => input.token, signal: AbortSignal.timeout(5000), onResponse: info => diagnostics.push(info) });
      const raw = await fetch(input.url+'/rest/v1/notes?select=id', { headers: { Authorization:'Bearer '+input.token, 'Accept-Profile':'api', Prefer:'count=exact' }, credentials:'omit' });
      const health = await fetch(input.url+'/healthz', { credentials:'omit' });
      const invalid = await readBrowserNotes({ url: input.url, subject: input.subject, accessToken: () => input.expired, signal: AbortSignal.timeout(5000) });
      return { diagnostics, ids: result.data?.map(row => row.id), error: result.error?.code ?? null, count: result.count, requestID: raw.headers.get('X-Request-Id'), range: raw.headers.get('Content-Range'), preference: raw.headers.get('Preference-Applied'), health: health.status, invalidStatus: invalid.status, invalidCode: invalid.error?.code };
    })()`)
    assert.equal(allowed.error, null)
    assert.equal(allowed.diagnostics.length, 1)
    assert.match(allowed.diagnostics[0].requestId, /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/)
    assert.ok(allowed.diagnostics[0].status >= 200 && allowed.diagnostics[0].status < 300)
    assert.ok(Number.isFinite(allowed.diagnostics[0].durationMs) && allowed.diagnostics[0].durationMs >= 0)
    assert.deepEqual(Object.keys(allowed.diagnostics[0]).sort(), ['durationMs', 'requestId', 'status'])

    assert.equal(allowed.count, 1, 'browser_count_must_respect_rls')
    assert.deepEqual(allowed.ids, [expectedID])
    assert.match(allowed.requestID, /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/, 'request_id_not_exposed')
    assert.ok(allowed.range, 'content_range_not_exposed')
    assert.ok(allowed.preference?.includes('count=exact'), 'preference_header_not_exposed')
    assert.equal(allowed.health, 200)
    assert.ok(requests.some(req => req.path === '/healthz' && req.origin === origins.allowed && !req.authorization && !req.cookie), 'health_requires_no_credentials')
    assert.equal(allowed.invalidStatus, 401)
    assert.equal(allowed.invalidCode, 'token_invalid')
    assert.ok(requests.some(req => req.origin === origins.allowed && req.method === 'OPTIONS' && !req.authorization), 'preflight_not_observed')
    assert.ok(requests.filter(req => req.origin === origins.allowed).every(req => !req.cookie), 'browser_credentials_leaked')
    const before = requests.length
    await navigate(origins.denied)
    const denied = await evaluate(`(async () => { const input=${input}; const {readBrowserNotes}=await import('/client.js'); const result=await readBrowserNotes({url:input.url,subject:input.subject,accessToken:()=>input.token,signal:AbortSignal.timeout(5000)}); return {status:result.status,hasError:Boolean(result.error)} })()`)
    assert.equal(denied.status, 0)
    assert.equal(denied.hasError, true)
    assert.ok(requests.slice(before).some(req => req.method === 'OPTIONS' && req.origin === origins.denied))
    assert.ok(!requests.slice(before).some(req => req.method === 'GET' && req.authorization), 'denied_browser_sent_token')
  } finally {
    gateway.off('request', capture)
    for (const item of pending.values()) clearTimeout(item.timer)
    socket?.close()
    child.kill('SIGKILL')
    if (!spawnError && child.exitCode === null && child.signalCode === null) await new Promise(resolve => child.once('exit', resolve))
    await rm(profile, { recursive: true, force: true })
  }
}
