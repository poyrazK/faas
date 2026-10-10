import { requireValue } from './checks.mjs'

const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i
const hash = /^[a-f0-9]{64}$/

export function typeFingerprint(content) {
  const match = /^\/\/ Schema fingerprint: ([a-f0-9]{64})$/m.exec(content)
  requireValue(match, 'generated_fingerprint_missing')
  return match[1]
}

export function verifiedSync(receipt, app, content) {
  requireValue(receipt.app === app.slug && receipt.status === 'completed' && receipt.contract_verified === true &&
    receipt.wake_id && receipt.task_id && receipt.deployment_id && hash.test(receipt.fingerprint) && receipt.fingerprint === typeFingerprint(content), 'sync_contract_not_verified')
  return { app: app.slug, wake_id: receipt.wake_id, task_id: receipt.task_id, deployment_id: receipt.deployment_id, fingerprint: receipt.fingerprint, contract_verified: true }
}

export function verifiedContract(contract, expected) {
  requireValue(contract.version === 1 && contract.ready === true && hash.test(contract.fingerprint) && contract.fingerprint === expected, 'serving_fingerprint_mismatch')
}

export function verifiedDiagnostic(info) {
  requireValue(info && uuid.test(info.requestId ?? '') && Number.isInteger(info.status) && info.status >= 100 && info.status <= 599 && Number.isFinite(info.durationMs) && info.durationMs >= 0 &&
    Object.keys(info).sort().join(',') === 'durationMs,requestId,status', 'sdk_diagnostics_invalid')
}

export function matchingRuntimeLog(output, info, deployment, method, route) {
  verifiedDiagnostic(info)
  for (const line of output.split('\n').filter(Boolean)) {
    let envelope, record
    try { envelope = JSON.parse(line); record = JSON.parse(envelope.line) } catch { continue }
    if (!record || typeof record !== 'object' || record.request_id !== info.requestId) continue
    const fields = ['code', 'duration_ms', 'event', 'level', 'method', 'outcome', 'request_id', 'route', 'status', 'time']
    requireValue(Object.keys(record).sort().join(',') === fields.join(',') && record.event === 'data_api_request' &&
      record.method === method && record.route === route && record.status === info.status && record.outcome === 'completed' && record.code === null && record.level === 'info' &&
      Number.isFinite(record.duration_ms) && record.duration_ms >= 0 && Number.isFinite(Date.parse(record.time)) && envelope.deployment_id === deployment &&
      typeof envelope.instance === 'string' && /^[a-f0-9-]{36}$/i.test(envelope.instance), 'runtime_log_contract_invalid')
    return { request_id: record.request_id, status: record.status, method, route, deployment_id: deployment, instance_id: envelope.instance }
  }
  return null
}
