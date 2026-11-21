import { pathToFileURL } from 'node:url'
import { writeFile } from 'node:fs/promises'

class JobHTTPError extends Error {
  constructor(status, code) { super('Background job request failed'); this.status = status; this.code = /^[A-Za-z0-9_]{1,64}$/.test(code ?? '') ? code : 'database_rejected' }
}
export async function runDataJobs({ url, key, fetchImpl = fetch, signal = AbortSignal.timeout(110000), maxJobs = 5, log = value => console.log(JSON.stringify(value)) }) {
  const base = new URL(url)
  if ((base.protocol !== 'https:' && !(base.protocol === 'http:' && ['localhost','127.0.0.1','[::1]'].includes(base.hostname))) || base.username || base.password || base.search || base.hash || base.pathname !== '/' || !/^gdsk_[A-Za-z0-9_-]{43}$/.test(key ?? '') || !Number.isInteger(maxJobs) || maxJobs < 1 || maxJobs > 5) throw new Error('Invalid data jobs worker configuration')
  async function rpc(name, args, actor) {
    const response = await fetchImpl(new URL(`/rest/v1/rpc/${name}`, base), { method: 'POST', redirect: 'error', credentials: 'omit', signal: AbortSignal.any([signal, AbortSignal.timeout(20000)]),
      headers: { authorization: `Bearer ${key}`, 'content-type': 'application/json', 'content-profile': 'api', ...(actor ? { 'x-gregale-subject': actor } : {}) }, body: JSON.stringify(args) })
    const chunks = []; let bytes = 0
    for await (const chunk of response.body ?? []) { bytes += chunk.length; if (bytes > 16777216) throw new Error('Job response exceeds bounds'); chunks.push(chunk) }
    const source = Buffer.concat(chunks).toString('utf8')
    let result = null
    try { result = source ? JSON.parse(source) : null } catch { throw new Error('Invalid job response') }
    if (!response.ok) throw new JobHTTPError(response.status, result?.code)
    return result
  }
  let completed = 0, failed = 0
  for (let count = 0; count < maxJobs; count++) {
    const claimed = await rpc('claim_team_order_job', {})
    if (!Array.isArray(claimed) || claimed.length > 1) throw new Error('Invalid job claim')
    if (!claimed.length) break
    let job = claimed[0]
    if (!job || typeof job.id !== 'string' || typeof job.actor !== 'string') throw new Error('Invalid job claim')
    if (job.state === 'blocked') { log({ event: 'data_api_job_blocked', job_id: job.id, attempts: job.attempts }); failed++; continue }
    if (typeof job.lease_token !== 'string') throw new Error('Invalid job claim')
    const claim = job.lease_token
    for (let step = 0; step < 60; step++) {
      signal.throwIfAborted()
      try {
        const advanced = await rpc('advance_team_order_job', { job_id: job.id, claim_token: claim }, job.actor)
        if (!Array.isArray(advanced) || advanced.length !== 1 || advanced[0].id !== job.id || advanced[0].actor !== job.actor) throw new Error('Invalid job progress')
        job = advanced[0]
        log({ event: 'data_api_job_progress', job_id: job.id, state: job.state, processed: job.processed, total: job.total })
        if (job.state === 'completed') { completed++; break }
      } catch (error) {
        // Network errors and 5xx can hide a committed batch. Leave its lease
        // recoverable; the saved import offset/receipt resolves the next run.
        if (!(error instanceof JobHTTPError) || ![400,401,403,404,409,412,413,422].includes(error.status)) {
          signal.throwIfAborted()
          const retried = await rpc('retry_team_order_job', { job_id: job.id, claim_token: claim, failure_code: error instanceof JobHTTPError ? error.code : 'request_uncertain' })
          if (!Array.isArray(retried) || retried.length !== 1 || retried[0].id !== job.id) throw new Error('Invalid job retry receipt')
          if (retried[0].state === 'completed') completed++
          else if (retried[0].state === 'blocked') { failed++; log({ event: 'data_api_job_blocked', job_id: job.id, attempts: retried[0].attempts }) }
          else log({ event: 'data_api_job_retry', job_id: job.id, state: retried[0].state, attempts: retried[0].attempts, next_attempt_at: retried[0].next_attempt_at })
          break
        }
        await rpc('fail_team_order_job', { job_id: job.id, claim_token: claim, failure_code: error.code })
        log({ event: 'data_api_job_failed', job_id: job.id, code: error.code })
        failed++; break
      }
      if (step === 59) throw new Error('Job step bound reached; lease remains recoverable')
    }
  }
  return { completed, failed }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  runDataJobs({ url: process.env.DATA_API_URL, key: process.env.DATA_API_SERVER_KEY }).then(async result => {
    if (process.env.GREGALE_OUTPUT_MANIFEST_PATH) await writeFile(process.env.GREGALE_OUTPUT_MANIFEST_PATH, JSON.stringify({ version: 1, artifacts: [], outcome_code: result.failed ? 'data_jobs_with_errors' : 'data_jobs_processed' }))
  }).catch(() => { console.error('Background data worker stopped; its lease and progress remain recoverable.'); process.exitCode = 1 })
}
