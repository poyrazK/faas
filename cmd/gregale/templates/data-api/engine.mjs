import http from 'node:http'
import { spawn } from 'node:child_process'
import { setTimeout as sleep } from 'node:timers/promises'

// The engine is the PostgREST process. A VM snapshot must not capture its SQL
// sessions: they are dead after a restore. before_checkpoint stops it,
// after_restore starts a fresh one, and requests wait for its readiness inside
// their own deadline. An engine stopped while the VM keeps running starts again
// on the next request.
export const engineStopGraceMs = 1500
// The CLI registers these as the app's lifecycle hooks (setDataAPILifecycle);
// TestDataAPIConfiguresBindingEgressAndScopedSecrets checks they match.
export const lifecyclePaths = Object.freeze({ beforeCheckpoint: '/__gregale/lifecycle/before-checkpoint', afterRestore: '/__gregale/lifecycle/after-restore' })
const probeMs = 1000, pollMinMs = 25, pollMaxMs = 250

export function createEngine({ bin, env, readyPort, onExit }) {
  let child = null, readiness = null
  function start() {
    if (child) return
    const proc = spawn(bin, [], { env, stdio: ['ignore', 'ignore', 'ignore'] })
    child = proc
    // Only the current process can end the server; a stopped one exits as asked.
    proc.once('error', error => { if (child === proc) { child = null; onExit({ error }) } })
    proc.once('exit', code => { if (child === proc) { child = null; onExit({ code }) } })
  }
  function probe() {
    return new Promise(resolve => {
      const request = http.get({ hostname: '127.0.0.1', port: readyPort, path: '/ready', agent: false, timeout: probeMs }, response => {
        response.resume()
        resolve(response.statusCode === 200)
      })
      request.on('timeout', () => request.destroy())
      request.on('error', () => resolve(false))
    })
  }
  // One shared poll per process; it ends when the process is ready or replaced.
  function poll() {
    const proc = child
    if (readiness?.proc === proc) return readiness.promise
    const state = { proc, ready: false }
    state.promise = (async () => {
      for (let wait = pollMinMs; child === proc; wait = Math.min(wait * 2, pollMaxMs)) {
        if (await probe()) return child === proc && (state.ready = true)
        await sleep(wait)
      }
      return false
    })()
    readiness = state
    return state.promise
  }
  return {
    start() { start(); void poll() },
    async ready(waitMs) {
      start()
      if (readiness?.proc === child && readiness.ready) return true
      if (waitMs <= 0) return false
      const timeout = new AbortController()
      try { return await Promise.race([poll(), sleep(waitMs, false, { signal: timeout.signal })]) } finally { timeout.abort() }
    },
    async stop(graceMs = engineStopGraceMs) {
      const proc = child
      if (!proc) return
      child = null
      readiness = null
      if (proc.exitCode !== null || proc.signalCode !== null) return
      const exited = new Promise(resolve => proc.once('exit', resolve))
      proc.kill('SIGTERM')
      const timer = setTimeout(() => proc.kill('SIGKILL'), graceMs)
      try { await exited } finally { clearTimeout(timer) }
    },
    signal(name) { child?.kill(name) },
  }
}
