import test from 'node:test'
import assert from 'node:assert/strict'
import { createTimings } from '../timing.mjs'

test('timing transitions retain completed phases and freeze interrupted observations', () => {
  const timing = createTimings()
  timing.enter('verification', 10)
  timing.enter('verification', 12)
  timing.enter('private-phase', 15)
  timing.enter('refresh', 20)
  const snapshot = timing.snapshot(35, false)
  assert.deepEqual(snapshot, {
    verification: { duration_ms: 10, complete: true },
    refresh: { duration_ms: 15, complete: false },
  })
  timing.enter('signing', 50)
  assert.deepEqual(timing.snapshot(100, true), snapshot)
  assert.equal(snapshot.refresh.duration_ms, 15)
  assert.equal(snapshot.refresh.complete, false)
})

test('repeated phases accumulate and zero duration is an observed sample', () => {
  const timing = createTimings()
  timing.enter('refresh', 0)
  timing.enter('signing', 1.125)
  timing.enter('refresh', 1.125)
  assert.deepEqual(timing.snapshot(3, true), {
    refresh: { duration_ms: 3, complete: true },
    signing: { duration_ms: 0, complete: true },
  })
})
