// Workflow starts for application Auth (ADR-1190 phase 5). After authorize()
// allows a user to start a workflow, the front publishes an app-scoped event
// (ADR-929) that the customer's workflow subscribes to. The key it publishes
// with holds only events:publish, so the Auth app cannot deploy or read.

export function workflowsConfig(env) {
  if (!env.AUTH_EVENTS_KEY) return null
  let api
  try { api = new URL(env.AUTH_GREGALE_API_URL ?? '') } catch { throw new Error('AUTH_GREGALE_API_URL is required with AUTH_EVENTS_KEY') }
  if (api.protocol !== 'https:' && !['localhost', '127.0.0.1'].includes(api.hostname)) throw new Error('AUTH_GREGALE_API_URL must be HTTPS')
  if (!/^[a-z0-9][a-z0-9-]{0,62}$/.test(env.AUTH_APP_SLUG ?? '')) throw new Error('AUTH_APP_SLUG is required with AUTH_EVENTS_KEY')
  return { api: api.origin, slug: env.AUTH_APP_SLUG, key: env.AUTH_EVENTS_KEY }
}

export class WorkflowError extends Error {
  constructor(status, code) { super(code); this.status = status; this.code = code }
}

export function createWorkflowStarter(config, fetchImpl = fetch) {
  return async ({ name, org, user, input, idempotencyKey }) => {
    const body = {
      // The key makes a retried start with the same idempotency key return
      // the original receipt instead of starting a second run.
      key: `workflow-start.${name}.${user}.${idempotencyKey}`,
      type: 'auth.workflow.start',
      data: { workflow: name, org_id: org, user_id: user, input: input ?? null },
    }
    let response
    try {
      response = await fetchImpl(`${config.api}/v1/apps/${config.slug}/events:publish`, {
        method: 'POST', redirect: 'error', signal: AbortSignal.timeout(10000),
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${config.key}` },
        body: JSON.stringify(body),
      })
    } catch {
      throw new WorkflowError(503, 'workflow_start_unavailable')
    }
    const text = await response.text()
    if (response.status === 409) throw new WorkflowError(409, 'idempotency_conflict')
    if (response.status === 429 || response.status >= 500) throw new WorkflowError(503, 'workflow_start_unavailable')
    if (!response.ok) throw new WorkflowError(502, 'workflow_start_rejected')
    let receipt = {}
    try { receipt = JSON.parse(text) } catch { /* acceptance does not depend on the body */ }
    return { accepted: true, duplicate: receipt.duplicate === true, source: receipt.source ?? null }
  }
}
