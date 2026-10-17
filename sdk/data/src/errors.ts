export type DataErrorCategory = 'unique_violation' | 'foreign_key_violation' | 'authentication' | 'permission_denied' | 'contract_mismatch' | 'timeout' | 'cancelled' | 'unavailable' | 'no_response' | 'response_too_large' | 'rate_limited' | 'unknown'
export type DataErrorRecovery = 'change_input' | 'authenticate' | 'check_permissions' | 'refresh_contract' | 'reconcile' | 'check_service' | 'wait' | 'inspect'
export type ClassifiedDataError<Error = unknown> = Readonly<{
  category: DataErrorCategory
  recovery: DataErrorRecovery
  code: string | null
  status: number | null
  /** The unchanged error may contain private database details; do not log blindly. */
  original: Error
}>

const categories: Readonly<Record<string, DataErrorCategory>> = Object.freeze({
  '23505': 'unique_violation',
  '23503': 'foreign_key_violation',
  '42501': 'permission_denied',
  token_required: 'authentication',
  token_invalid: 'authentication',
  PGRST301: 'authentication',
  PGRST302: 'authentication',
  PGRST303: 'authentication',
  origin_not_allowed: 'permission_denied',
  schema_contract_mismatch: 'contract_mismatch',
  query_timeout: 'timeout',
  request_deadline_exceeded: 'timeout',
  response_too_large: 'response_too_large',
  request_timeout: 'timeout',
  request_cancelled: 'cancelled',
  '57014': 'cancelled',
  data_api_unavailable: 'unavailable',
  data_api_overloaded: 'unavailable',
  data_api_rate_limited: 'rate_limited',
  serving_contract_unavailable: 'unavailable',
  data_api_refresh_unavailable: 'unavailable',
})

const recoveries: Record<DataErrorCategory, DataErrorRecovery> = {
  unique_violation: 'change_input', foreign_key_violation: 'change_input',
  authentication: 'authenticate', permission_denied: 'check_permissions',
  contract_mismatch: 'refresh_contract', timeout: 'reconcile', cancelled: 'reconcile',
  unavailable: 'check_service', no_response: 'reconcile', response_too_large: 'reconcile', rate_limited: 'wait', unknown: 'inspect',
}

/** Classify without executing, retrying or changing an error. HTTP status is
 * optional because throwOnError() exceptions do not retain response status.
 * Recovery is guidance, never permission to replay a mutation.
 */
export function classifyDataError<Error>(error: Error, options: { status?: number } = {}): ClassifiedDataError<NonNullable<Error>> | null {
  if (error === null || error === undefined) return null
  const rawCode = typeof error === 'object' && 'code' in error ? error.code : undefined
  const code = typeof rawCode === 'string' ? rawCode : null
  const status = Number.isInteger(options.status) && (options.status === 0 || (options.status! >= 100 && options.status! <= 599)) ? options.status! : null
  // Own-property lookup prevents arbitrary codes such as "constructor" from
  // acquiring a category. Messages, hints and details never drive classification.
  let category: DataErrorCategory = code !== null && Object.hasOwn(categories, code) ? categories[code] : 'unknown'
  if (category === 'unknown') {
    if (status === 401) category = 'authentication'
    else if (status === 403) category = 'permission_denied'
    else if (status === 429) category = 'rate_limited'
    else if (status === 503) category = 'unavailable'
    else if (status === 504) category = 'timeout'
    else if (status === 0) category = 'no_response'
  }
  return Object.freeze({ category, recovery: recoveries[category], code, status, original: error as NonNullable<Error> })
}
