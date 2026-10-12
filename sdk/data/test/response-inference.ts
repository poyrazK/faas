import { createDataClient, validatedRpc, validateRpcResponse, type RpcResponseValidationError, type ValidatedRpcResponse } from '../src/index.js'
type Database = { api: { Tables: {}; Views: {}; Functions: { count_notes: { Args: Record<PropertyKey, never>; Returns: number }; get_config: { Args: Record<PropertyKey, never>; Returns: unknown } } } }
const db = createDataClient<Database>({ url: 'https://data.example', accessToken: 'token' }).schema('api')
const parser = { parse(value: unknown): { total: number } {
  if (typeof value !== 'number') throw new Error()
  return { total: value }
} }
const result = await db.rpc('count_notes', {}).retry(false)
const checked = validateRpcResponse(result, parser)
const typed: ValidatedRpcResponse<typeof result, { total: number }> = checked
if (checked.outcome === 'validated') {
  const total: number = checked.result.data.total
  const error: null = checked.result.error
  const status: number = checked.result.status
  const count: number | null = checked.result.count
  // @ts-expect-error parser replaces the scalar with its output type
  const scalar: number = checked.result.data
  void [total, error, status, count, scalar]
} else if (checked.outcome === 'request_failed') {
  const original: typeof result = checked.result
  void original
} else {
  const error: RpcResponseValidationError = checked.error
  const status: number = checked.status
  // @ts-expect-error rejected raw response is not exposed in a validation failure
  checked.result
  void [error, status]
}
const outcome = await validatedRpc({}, { parse: () => ({}) }, args => db.rpc('get_config', args).retry(false))
if (outcome.outcome === 'completed') {
  const parsed = validateRpcResponse(outcome.result, { parse: (_value: unknown): { theme: 'light' | 'dark' } => ({ theme: 'light' }) })
  if (parsed.outcome === 'validated') {
    const theme: 'light' | 'dark' = parsed.result.data.theme
    void theme
  }
}
// @ts-expect-error synchronous response parsers only
validateRpcResponse(result, { parse: async () => 42 })
const nullable = validateRpcResponse(result, { parse: (_value: unknown): number | null => null })
if (nullable.outcome === 'validated') {
  const value: number | null = nullable.result.data
  // @ts-expect-error parser output stays nullable
  const required: number = nullable.result.data
  void [value, required]
}
void typed
