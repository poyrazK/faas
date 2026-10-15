import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

export async function browserInspector(url, check) {
  const profile = await mkdtemp(join(tmpdir(), 'gregale-inspector-browser-'))
  const processGroup = process.platform !== 'win32'
  const child = spawn(process.env.DATA_API_CHROMIUM_BIN, ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--no-first-run', '--disable-background-networking', '--disk-cache-size=1', '--remote-debugging-address=127.0.0.1', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { detached: processGroup, stdio: ['ignore', 'ignore', 'pipe'] })
  const closed = new Promise(resolve => child.once('close', resolve))
  let socket, spawnError, starting = true, startupStderr = ''
  child.stderr.on('data', chunk => { if (starting && startupStderr.length < 16384) startupStderr += chunk.toString().slice(0, 16384 - startupStderr.length) })
  const pending = new Map()
  child.on('error', error => { spawnError = error })
  try {
    let port
    const deadline = Date.now() + 30000
    while (Date.now() < deadline) {
      if (spawnError || child.exitCode !== null || child.signalCode !== null) break
      try {
        // Chromium can create the file before writing its contents. Wait for
        // a terminated port line rather than accepting an empty/partial read.
        const contents = await readFile(join(profile, 'DevToolsActivePort'), 'utf8')
        const match = /^([1-9]\d{0,4})\r?\n/.exec(contents)
        if (match && Number(match[1]) <= 65535) { port = match[1]; break }
      } catch {}
      await new Promise(resolve => setTimeout(resolve, 50))
    }
    assert.equal(spawnError, undefined, 'chromium_failed_to_start')
    // Startup precedes navigation and token injection, so these bounded logs
    // cannot contain application credentials or authenticated page requests.
    starting = false
    assert.ok(port, `chromium_not_ready (exit=${child.exitCode}, signal=${child.signalCode}): ${startupStderr}`)
    const targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
    const page = targets.find(target => target.type === 'page')
    socket = new WebSocket(page.webSocketDebuggerUrl)
    await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }) })
    let serial = 0
    const parsedDocuments = new Set()
    socket.addEventListener('message', event => {
      const data = JSON.parse(event.data)
      if (data.method === 'Page.lifecycleEvent' && data.params.name === 'DOMContentLoaded') parsedDocuments.add(`${data.params.frameId}:${data.params.loaderId}`)
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
    await send('Page.enable')
    await send('Page.setLifecycleEventsEnabled', { enabled: true })
    const navigation = await send('Page.navigate', { url })
    assert.ok(!navigation.errorText, 'browser_navigation_failed')
    // Navigation is acknowledged before the new document has finished parsing.
    // Match its loader so an event from about:blank or an iframe cannot release
    // the check, and retain events that arrive before the navigation response.
    const documentKey = `${navigation.frameId}:${navigation.loaderId}`
    const documentDeadline = Date.now() + 10000
    while (navigation.loaderId && !parsedDocuments.has(documentKey) && Date.now() < documentDeadline) await new Promise(resolve => setTimeout(resolve, 25))
    assert.ok(!navigation.loaderId || parsedDocuments.has(documentKey), 'browser_document_not_ready')
    await check(evaluate)
  } finally {
    for (const item of pending.values()) clearTimeout(item.timer)
    socket?.close()
    // Chromium's renderer and utility processes also write the profile. Stop
    // the entire isolated group, then wait for inherited stderr to close.
    if (processGroup && child.pid) {
      try { process.kill(-child.pid, 'SIGKILL') } catch (error) { if (error.code !== 'ESRCH') throw error }
    } else child.kill('SIGKILL')
    await closed
    await rm(profile, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 })
  }
}
