import test from 'node:test'
import assert from 'node:assert/strict'
import { PassThrough } from 'node:stream'
import { once } from 'node:events'
import { filterLogs, logLine } from '../logs.mjs'

test('drops GoTrue info and debug lines and keeps everything else', async () => {
  const input = new PassThrough()
  const output = new PassThrough()
  const written = []
  output.on('data', chunk => written.push(chunk.toString()))
  const lines = filterLogs(input, output)
  input.end([
    '{"level":"info","msg":"request completed","path":"/signup"}',
    '{"auth_audit_event":{"actor_username":"alice@example.test"},"level":"info","msg":"audit_event"}',
    '{"level":"debug","msg":"x"}',
    '{"level":"warning","msg":"rate limit header missing"}',
    '{"level":"error","msg":"database unavailable"}',
    'panic: runtime error',
    '{"broken json',
  ].join('\n') + '\n')
  await once(lines, 'close')
  const text = written.join('')
  assert.doesNotMatch(text, /alice@example\.test|request completed|"debug"/)
  for (const kept of ['rate limit header missing', 'database unavailable', 'panic: runtime error', '{"broken json']) assert.match(text, new RegExp(kept.replace(/[{"]/g, '.')))
  assert.equal(logLine('{"level":"fatal","msg":"x"}'), '{"level":"fatal","msg":"x"}')
  const redacted = JSON.parse(logLine('{"level":"error","msg":"Hook errored out","auth_event":{"action":"user_confirmation_requested","actor_username":"alice@example.test","traits":{"email":"alice@example.test"}},"path":"/signup"}'))
  assert.equal(redacted.auth_event.actor_username, '[redacted]')
  assert.equal(redacted.auth_event.traits.email, '[redacted]')
  assert.equal(redacted.auth_event.action, 'user_confirmation_requested')
  assert.equal(redacted.path, '/signup')
})
