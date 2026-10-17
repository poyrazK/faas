export const timingPhases = Object.freeze(['verification', 'refresh', 'readiness', 'signing', 'upstream', 'streaming'])

// Only admitted gateway phases are measured. An interrupted phase is an elapsed
// lower bound, not a completed latency sample or PostgreSQL execution timing.
export function createTimings() {
  const measurements = {}
  let active, started, closed = false
  function finish(now, complete) {
    if (!active) return
    const previous = measurements[active]
    measurements[active] = {
      duration_ms: Math.round(((previous?.duration_ms ?? 0) + Math.max(0, now - started)) * 1000) / 1000,
      complete: (previous?.complete ?? true) && complete,
    }
  }
  return {
    enter(phase, now) {
      if (closed || !timingPhases.includes(phase) || active === phase) return
      finish(now, true)
      active = phase; started = now
    },
    snapshot(now, complete) {
      if (!closed) { finish(now, complete); closed = true }
      return measurements
    },
  }
}
