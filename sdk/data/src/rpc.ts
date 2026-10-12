import type { JsonParser } from './validation.js'

/** Omits input and parser exceptions, which can contain private values. */
export class RpcValidationError extends Error {
  readonly code = 'RPC_ARGUMENT_VALIDATION_FAILED'
  constructor() {
    super('RPC argument validation failed')
    this.name = 'RpcValidationError'
  }
}

export type ValidatedRpcResult<Result> =
  | { outcome: 'validation_failed'; error: RpcValidationError }
  | { outcome: 'completed'; result: Result }

/** Parse untrusted arguments before constructing or executing an RPC query.
 * The application parser owns validation, defaults and transformations. The
 * query callback retains generated RPC argument and result inference, and may
 * apply projections, cancellation or explicit retry settings. Server errors
 * remain in the original result; thrown request errors propagate unchanged.
 */
export async function validatedRpc<Args extends object, Query extends PromiseLike<unknown>>(
  input: unknown,
  parser: JsonParser<Args> & (Extract<Args, PromiseLike<unknown>> extends never ? unknown : never),
  createQuery: (args: Args) => Query,
): Promise<ValidatedRpcResult<Awaited<Query>>> {
  let args: Args
  try {
    args = parser.parse(input)
    if (args !== null && (typeof args === 'object' || typeof args === 'function') && typeof (args as { then?: unknown }).then === 'function') {
      void Promise.resolve(args).catch(() => {})
      throw new Error()
    }
    if (args === null || typeof args !== 'object' || Array.isArray(args)) throw new Error()
  } catch {
    return { outcome: 'validation_failed', error: new RpcValidationError() }
  }
  // Deliberately outside the parser catch: request failures are not validation failures.
  return { outcome: 'completed', result: await createQuery(args) }
}
