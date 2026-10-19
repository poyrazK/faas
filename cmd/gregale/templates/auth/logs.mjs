import { createInterface } from 'node:readline'

// GoTrue v2.197.0 logs requests and audit events (with user email addresses)
// at info even when GOTRUE_LOG_LEVEL is set, so the front filters its JSON
// log lines: warnings and errors pass, info and debug are dropped. Lines that
// are not GoTrue JSON pass unchanged so crashes stay visible.
const quiet = new Set(['trace', 'debug', 'info'])
// Warnings and errors still name the user (auth_event.actor_username); keep
// the event, not the person.
const personal = new Set(['actor_username', 'email', 'new_email', 'phone', 'new_phone'])
// GoTrue warns at every start about the admin group, which it defaults
// itself, and the default group, which config.mjs sets on purpose.
const expected = new Set(['ADMIN', 'DEFAULT'].map(name => `DEPRECATION NOTICE: GOTRUE_JWT_${name}_GROUP_NAME not supported by Supabase's GoTrue, will be removed soon`))

function redact(value) {
  if (Array.isArray(value)) return value.map(redact)
  if (!value || typeof value !== 'object') return value
  return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, personal.has(k) && v !== '' ? '[redacted]' : redact(v)]))
}

// logLine returns the line to write, or null to drop it.
export function logLine(line) {
  if (!line.startsWith('{')) return line
  let entry
  try { entry = JSON.parse(line) } catch { return line }
  if (typeof entry.level === 'string' && quiet.has(entry.level)) return null
  if (entry.level === 'warning' && expected.has(entry.msg)) return null
  return JSON.stringify(redact(entry))
}

export function filterLogs(input, output) {
  const lines = createInterface({ input, crlfDelay: Infinity })
  lines.on('line', line => {
    const kept = logLine(line)
    if (kept !== null) output.write(kept + '\n')
  })
  return lines
}
