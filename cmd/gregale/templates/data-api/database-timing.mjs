import { limits } from './config.mjs'

export const databaseTimingPhases = Object.freeze(['jwt', 'parse', 'plan', 'transaction', 'response'])

const contract = () => ({ version: 1, source: 'postgrest_server_timing', sql_execution: 'unknown', pool_wait: 'unknown' })

export const unknownDatabaseTimings = reason => ({ ...contract(), state: 'unknown', reason })

// PostgREST 14.3 renders exactly five unlabelled, millisecond durations with
// one decimal place. Its transaction timer wraps usePool: acquisition, setup,
// hooks, SQL and result transfer are combined. Never derive SQL or pool wait by
// subtracting this timer from gateway time. Error paths may omit the header.
export function parseDatabaseTimings(header) {
  if (header === undefined) return unknownDatabaseTimings('missing_header')
  const invalid = () => unknownDatabaseTimings('invalid_header')
  if (typeof header !== 'string' || Buffer.byteLength(header) > limits.healthBytes) return invalid()
  const phases = {}
  for (const part of header.split(',')) {
    const match = /^(jwt|parse|plan|transaction|response);dur=(\d+\.\d)$/.exec(part.trim())
    if (!match || Object.hasOwn(phases, match[1]) || !Number.isSafeInteger(Number(match[2].replace('.', '')))) return invalid()
    phases[match[1]] = { duration_ms: Number(match[2]), complete: true }
  }
  if (Object.keys(phases).length !== databaseTimingPhases.length) return invalid()
  return { ...contract(), state: 'observed', phases }
}
