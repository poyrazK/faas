import { limits } from './config.mjs'
import { markDeadline, markPhase } from './logging.mjs'

// The existing query budget covers all admitted work, including response
// streaming. A socket inactivity timeout alone can be extended by every chunk.
export function createDeadline(req, res, expire) {
  const started = performance.now(), controller = new AbortController()
  const expiresAt = Date.now() + limits.queryMs
  let phase = 'verification', dispatched = false, stopped = false, timer
  markPhase(res, phase)
  function stop() {
    if (stopped) return
    stopped = true
    clearTimeout(timer)
    req.removeListener('aborted', stop)
    res.removeListener('finish', stop)
    res.removeListener('close', stop)
    controller.abort()
  }
  function check() {
    if (stopped) return false
    if (req.aborted || res.destroyed || res.writableEnded) { stop(); return false }
    const elapsed = performance.now() - started
    if (elapsed < limits.queryMs) return true
    markDeadline(res, { phase, elapsed_ms: Math.round(elapsed * 1000) / 1000, dispatched })
    // Emit the timeout response before aborting the engine request, whose error
    // handler must not turn this into a generic availability failure.
    expire()
    stop()
    return false
  }
  function arm() {
    if (!check()) return
    timer = setTimeout(arm, Math.max(1, Math.ceil(limits.queryMs - (performance.now() - started))))
    timer.unref()
  }
  req.once('aborted', stop)
  res.once('finish', stop)
  res.once('close', stop)
  arm()
  return { signal: controller.signal, check, expiresAt, stop,
    enter(value) { if (!check()) return false; phase = value; markPhase(res, value); return true },
    dispatch() { if (!check()) return false; dispatched = true; return true },
  }
}
