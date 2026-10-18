import type { LiveQueryText } from './seal.js'

/**
 * Builds a parameterised query: each interpolated value becomes $n and
 * travels as PostgreSQL text, which the database parses as the parameter's
 * type. Values never become SQL.
 *
 *   sql`SELECT id, body FROM messages WHERE channel_id = ${id} ORDER BY id DESC LIMIT ${50}`
 */
export function sql(strings: TemplateStringsArray, ...values: unknown[]): LiveQueryText {
  let text = strings[0]
  const params: (string | null)[] = []
  values.forEach((value, i) => {
    params.push(toPostgresText(value))
    text += `$${params.length}` + strings[i + 1]
  })
  return Object.freeze({ text, params: Object.freeze(params) })
}

/** PostgreSQL text form of a JavaScript value. */
export function toPostgresText(value: unknown): string | null {
  if (value === null || value === undefined) return null
  switch (typeof value) {
    case 'string':
      return value
    case 'number':
      if (!Number.isFinite(value)) throw new TypeError('live query parameters must be finite numbers')
      return String(value)
    case 'bigint':
      return value.toString()
    case 'boolean':
      return value ? 'true' : 'false'
  }
  if (value instanceof Date) {
    if (Number.isNaN(value.getTime())) throw new TypeError('invalid Date parameter')
    return value.toISOString()
  }
  if (value instanceof Uint8Array) {
    return '\\x' + Array.from(value, (b) => b.toString(16).padStart(2, '0')).join('')
  }
  if (Array.isArray(value)) return arrayLiteral(value)
  if (typeof value === 'object') return JSON.stringify(value)
  throw new TypeError(`unsupported live query parameter of type ${typeof value}`)
}

// A PostgreSQL array literal; elements are quoted unless NULL.
function arrayLiteral(values: unknown[]): string {
  const items = values.map((v) => {
    if (Array.isArray(v)) return arrayLiteral(v)
    const text = toPostgresText(v)
    if (text === null) return 'NULL'
    return '"' + text.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"'
  })
  return '{' + items.join(',') + '}'
}
