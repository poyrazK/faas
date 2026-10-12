import type { JsonParser } from './validation.js'

/** Omits rejected response values and parser exceptions, which may be private. */
export class RpcResponseValidationError extends Error {
  readonly code = 'RPC_RESPONSE_VALIDATION_FAILED'
  constructor() {
    super('RPC response validation failed')
    this.name = 'RpcResponseValidationError'
  }
}

type RpcResponse = { data: unknown; error: unknown; status: number }
type ParsedResponse<Result, Output> = Result extends unknown
  ? Omit<Result, 'data' | 'error'> & { data: Output; error: null }
  : never

export type ValidatedRpcResponse<Result, Output> =
  | { outcome: 'validated'; result: ParsedResponse<Result, Output> }
  | { outcome: 'request_failed'; result: Result }
  | { outcome: 'validation_failed'; error: RpcResponseValidationError; status: number }

/** Validate the entire data value of an already completed RPC response.
 * Null/undefined successful data goes through the application-owned parser.
 * Error/non-2xx results are returned unchanged without parsing. No requests or
 * retries occur; response validation failure does not prove a write rolled back.
 */
export function validateRpcResponse<Result extends RpcResponse, Output>(
  result: Result,
  parser: JsonParser<Output> & (Extract<Output, PromiseLike<unknown>> extends never ? unknown : never),
): ValidatedRpcResponse<Result, Output> {
  if (result.error !== null || result.status < 200 || result.status >= 300) {
    return { outcome: 'request_failed', result }
  }
  try {
    const data = parser.parse(result.data)
    if (data !== null && (typeof data === 'object' || typeof data === 'function') && typeof (data as { then?: unknown }).then === 'function') {
      void Promise.resolve(data).catch(() => {})
      throw new Error()
    }
    return { outcome: 'validated', result: { ...result, data, error: null } as ParsedResponse<Result, Output> }
  } catch {
    return { outcome: 'validation_failed', error: new RpcResponseValidationError(), status: result.status }
  }
}
