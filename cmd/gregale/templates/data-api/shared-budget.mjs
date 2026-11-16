import { createHash } from 'node:crypto'
import { limits } from './config.mjs'

export function sharedBudgetConfig(env) {
  if (env.DATA_API_SHARED_BUDGET === undefined) return null
  const invalid = () => { throw new Error('Invalid DATA_API_SHARED_BUDGET; use version, requests, window_seconds and optional claim') }
  let policy, origin, identity
  try { policy = JSON.parse(env.DATA_API_SHARED_BUDGET); origin = new URL(env.DATA_API_USAGE_URL); identity = new URL(env.FAAS_WORKLOAD_IDENTITY_ENDPOINT) } catch { return invalid() }
  if (!policy || Array.isArray(policy) || policy.version !== 1 || Object.keys(policy).some(k=>!['version','requests','window_seconds','claim'].includes(k)) || !Number.isSafeInteger(policy.requests) || policy.requests < 1 || policy.requests > limits.sharedBudgetMaxRequests || !Number.isSafeInteger(policy.window_seconds) || policy.window_seconds < 1 || policy.window_seconds > limits.sharedBudgetMaxWindowSeconds || policy.claim !== undefined && (typeof policy.claim !== 'string' || !/^[A-Za-z][A-Za-z0-9_]{0,63}$/.test(policy.claim))) return invalid()
  if (origin.protocol !== 'https:' || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash || identity.protocol !== 'http:' || !['127.0.0.1','[::1]'].includes(identity.hostname) || identity.pathname !== '/oidc/token' || identity.username || identity.password || identity.search || identity.hash) return invalid()
  identity.searchParams.set('audience','gregale:data-api:budget')
  return Object.freeze({...policy, endpoint:new URL('/v1/runtime/data-api/budget',origin).href,identity:identity.href})
}

// No cached grants: snapshots cannot duplicate shared request credit.
export function createSharedBudget(policy, fetcher=fetch) {
  if (!policy) return null
  return async (claims, signal) => {
    const tenant = policy.claim === undefined ? claims.sub : claims[policy.claim]
    if (typeof tenant !== 'string' || !tenant || Buffer.byteLength(tenant) > limits.serverKeySubjectMaxBytes) return {status:403,code:'data_api_budget_identity_required'}
    const key=createHash('sha256').update(JSON.stringify([policy.claim??'sub',tenant,policy.requests,policy.window_seconds])).digest('hex')
    const budgetSignal=AbortSignal.any([signal,AbortSignal.timeout(limits.sharedBudgetMs)])
    try {
      const identity=await fetcher(policy.identity,{signal:budgetSignal,redirect:'error'})
      if(!identity.ok) {await identity.body?.cancel();throw new Error()}
      const assertion=await boundedJSON(identity)
      if(assertion.token_type!=='Bearer'||typeof assertion.access_token!=='string'||assertion.access_token.length>limits.healthBytes)throw new Error()
      const response=await fetcher(policy.endpoint,{method:'POST',signal:budgetSignal,redirect:'error',headers:{Authorization:`Bearer ${assertion.access_token}`,'Content-Type':'application/json'},body:JSON.stringify({version:1,key,requests:policy.requests,window_seconds:policy.window_seconds})})
      if(!response.ok){await response.body?.cancel();throw new Error()}
      const decision=await boundedJSON(response)
      if(typeof decision.allowed!=='boolean'||!Number.isSafeInteger(decision.retry_after_seconds)||decision.retry_after_seconds<1||decision.retry_after_seconds>policy.window_seconds)throw new Error()
      return decision.allowed ? null : {status:429,code:'data_api_rate_limited',retry:decision.retry_after_seconds}
    } catch { return {status:503,code:'data_api_shared_budget_unavailable'} }
  }
}

async function boundedJSON(response) {
  if (!response.body) throw new Error()
  const reader=response.body.getReader(), chunks=[];let bytes=0
  try {
    while(true){const {value,done}=await reader.read();if(done)break;bytes+=value.byteLength;if(bytes>limits.healthBytes)throw new Error();chunks.push(value)}
    return JSON.parse(Buffer.concat(chunks).toString('utf8'))
  } finally { await reader.cancel().catch(()=>{}) }
}
