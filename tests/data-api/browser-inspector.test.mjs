import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { setTimeout } from 'node:timers/promises'
import test from 'node:test'
import { browserInspector } from './browser-inspector.mjs'

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
