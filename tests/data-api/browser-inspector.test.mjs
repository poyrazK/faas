import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { setTimeout } from 'node:timers/promises'
import test from 'node:test'
import { browserInspector } from './browser-inspector.mjs'
import { waitUntil } from './browser-wait.mjs'
import { inspector } from '../../cmd/gregale/templates/data-api/dev-inspector.mjs'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const browser = { skip: !process.env.DATA_API_CHROMIUM_BIN, timeout: 45000 }

async function serve(t, handler) {
  const server = createServer(handler)
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    server.closeAllConnections()
    await new Promise(resolve => server.close(resolve))
  })
  return `http://127.0.0.1:${server.address().port}`
}

test('browser inspector waits for the navigated document to finish parsing', browser, async t => {
  const origin = await serve(t, async (request, response) => {
    if (request.url === '/frame') {
      response.writeHead(200, { 'Content-Type': 'text/html' }).end('<!doctype html><p>frame ready</p>')
      return
    }
    if (request.url !== '/') { response.writeHead(404).end(); return }
    response.writeHead(200, { 'Content-Type': 'text/html' })
    response.write('<!doctype html><html><body><iframe src="/frame"></iframe>')
    await setTimeout(1000)
    response.end('<p id="status">ready</p></body></html>')
  })
  await browserInspector(origin, async evaluate => {
    assert.equal(await evaluate("document.querySelector('#status').textContent"), 'ready')
    assert.equal(await evaluate('document.readyState === "loading"'), false)
  })
})

test('browser inspector still propagates evaluation failures after navigation', browser, async t => {
  const origin = await serve(t, (request, response) => {
    response.writeHead(200, { 'Content-Type': 'text/html' }).end('<!doctype html><p>ready</p>')
  })
  await assert.rejects(browserInspector(origin, evaluate => evaluate('throw new Error("regression")')), /browser_evaluation_failed/)
})

test('session refresh compares identity context after decoding its response', browser, async t => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-inspector-session-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const table = { schema: 'api', name: 'notes', columns: ['subject', 'id', 'version', 'body'].map(name => ({ name, type: ['id', 'version'].includes(name) ? 'number' : 'string' })), primary_key: { columns: ['subject', 'id'] } }
  const model = { schema: 'api', table: 'notes', fields: [], primaryKey: { filterable: true, columns: ['subject', 'id'] }, concurrency: { kind: 'integer_version', column: 'version' }, updateFields: [{ name: 'body' }], updateSpec: { kind: 'object', fields: [{ name: 'body', optional: true, nullable: false, value: { kind: 'string' } }] } }
  const state = { ready: true, status: 'ready', fingerprint: 'a'.repeat(64), snapshot: { tables: [table], functions: [] }, input_model: { revision: 'initial', tables: [model] }, identities: { alice: { subject: 'alice', token: 'synthetic-alice' }, bob: { subject: 'bob', token: 'synthetic-bob' } } }
  const page = await inspector(() => state, join(directory, 'requests.json'), ['alice', 'bob'])
  state.url = await serve(t, (request, response) => {
    if (page.handle(request, response)) return
    assert.equal(request.method, 'GET', 'matching previews must never write')
    const rows = request.headers.authorization === 'Bearer synthetic-bob' || new URL(request.url, state.url).searchParams.get('offset') !== '0' ? [] : [{ subject: 'alice', id: 1, version: 1, body: 'before' }]
    response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(rows))
  })
  await browserInspector(page.url(state.url), async evaluate => {
    const until = expression => waitUntil(evaluate, expression, 'Session refresh did not settle')
    await until("document.querySelector('#resource').options.length === 1 && !document.querySelector('#data-read').disabled")
    // Hold the next periodic session response at its asynchronous JSON boundary.
    await evaluate(`window.sessionFetch = window.fetch; window.fetch = async (url, options) => {
      const response = await window.sessionFetch(url, options);
      if (String(url) === 'session' && !window.sessionHeld) {
        window.sessionHeld = true;
        const json = response.json.bind(response);
        response.json = async () => { window.sessionDecoding = true; await new Promise(resolve => { window.releaseSession = resolve }); return json() };
      }
      return response;
    }`)
    await until('window.sessionDecoding === true')
    await evaluate("document.querySelector('#identity').value='bob';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#bulk-scope').value='matching';document.querySelector('#bulk-scope').dispatchEvent(new Event('input'));document.querySelector('#bulk-changes').value=JSON.stringify({body:'after'});document.querySelector('#bulk-changes').dispatchEvent(new Event('input'));document.querySelector('#bulk-review').click()")
    await until("document.querySelector('#bulk-stop').disabled && document.querySelector('#bulk-status').textContent.includes('No rows match')")
    await evaluate("new Promise(resolve => { const observer = new MutationObserver(() => { observer.disconnect(); resolve() }); observer.observe(document.querySelector('#status'), { childList: true }); window.releaseSession() }).then(() => { window.fetch=window.sessionFetch })")
    // The held poll has rendered. Its old identity must not clear the result
    // produced under the newly selected identity.
    await until("document.querySelector('#status').textContent.includes('ready') && !document.querySelector('#bulk-review').disabled")
    assert.match(await evaluate("document.querySelector('#bulk-status').textContent"), /No rows match/)
    assert.equal(await evaluate("document.querySelector('#bulk-run').disabled"), true)
    const previewAlice = async () => {
      await evaluate("document.querySelector('#identity').value='alice';document.querySelector('#identity').dispatchEvent(new Event('change'));document.querySelector('#bulk-review').click()")
      await until("document.querySelector('#bulk-stop').disabled && document.querySelector('#bulk-preview').textContent.includes('expectedVersion')")
    }
    await previewAlice()
    state.input_model.revision = 'changed'
    await until("document.querySelector('#bulk-preview').textContent === ''")
    assert.equal(await evaluate("document.querySelector('#bulk-run').disabled"), true)
    await previewAlice()
    state.fingerprint = 'b'.repeat(64)
    await until("document.querySelector('#fingerprint').textContent.includes('bbbb') && document.querySelector('#bulk-preview').textContent === ''")
    assert.equal(await evaluate("document.querySelector('#bulk-run').disabled"), true)
  })
})
