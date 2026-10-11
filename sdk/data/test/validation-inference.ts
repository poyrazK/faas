import { validateJson, type Json } from '../src/index.js'
const parser = { parse(value: unknown): { count: number } {
  if (!value || typeof value !== 'object' || !('count' in value) || typeof value.count !== 'number') throw new Error()
  return { count: value.count }
} }
const rows = validateJson([{ id: 1, details: null as Json }], { details: parser })
const count: number = rows[0].details.count
const id: number = rows[0].id
rows[0].details = { count: 3 }
// @ts-expect-error Parser output replaces broad JSON.
rows[0].details = 'invalid'
// @ts-expect-error Selected fields must exist.
validateJson([{ id: 1 }], { missing: parser })
const single = validateJson(null as { details: Json } | null, { details: parser })
const nullable: { details: { count: number } } | null = single
// @ts-expect-error Missing single rows stay nullable.
const required: { details: { count: number } } = single
void [count, id, nullable, required]

// @ts-expect-error Async parser output is unsupported.
validateJson({ details: null as Json }, { details: { parse: async () => ({ count: 1 }) } })
