// Shared by the inspector and local CLI; no filesystem or authentication state.
export function matchesJSON(actual, expected) {
  if (Object.is(actual, expected)) return true
  if (!actual || !expected || typeof actual !== 'object' || typeof expected !== 'object' || Array.isArray(actual) !== Array.isArray(expected)) return false
  return Object.keys(actual).length === Object.keys(expected).length && Object.keys(expected).every(key => Object.hasOwn(actual, key) && matchesJSON(actual[key], expected[key]))
}
class VariableFailure extends Error {}
const variablePattern = /^[A-Za-z][A-Za-z0-9_]{0,63}$/
function substitute(value, variables, path = false) {
  if (typeof value === 'string') {
    const lookup = name => {
      if (!variablePattern.test(name) || !variables.has(name)) throw new VariableFailure(`Missing replay variable: ${name}.`)
      return variables.get(name)
    }
    const exact = value.match(/^\{\{([A-Za-z][A-Za-z0-9_]{0,63})\}\}$/)
    if (exact && !path) return lookup(exact[1])
    const template = value.replace(/\{\{([A-Za-z][A-Za-z0-9_]{0,63})\}\}/g, '')
    if (template.includes('{{') || template.includes('}}')) throw new VariableFailure('Invalid replay variable template; use {{variable_name}}.')
    return value.replace(/\{\{([A-Za-z][A-Za-z0-9_]{0,63})\}\}/g, (_, name) => path ? encodeURIComponent(String(lookup(name))) : String(lookup(name)))
  }
  if (Array.isArray(value)) return value.map(item => substitute(item, variables))
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, substitute(item, variables)]))
  return value
}
function captureValue(json, pointer, name) {
  let value = json
  for (const part of pointer === '' ? [] : pointer.slice(1).split('/')) {
    const key = part.replace(/~1/g, '/').replace(/~0/g, '~')
    if (value === null || typeof value !== 'object' || !Object.hasOwn(value, key)) throw new VariableFailure(`Cannot capture ${name}: response pointer is missing.`)
    value = value[key]
  }
  if ((value !== null && !['string', 'number', 'boolean'].includes(typeof value)) || (typeof value === 'string' && value.length > 4096)) throw new VariableFailure(`Cannot capture ${name}: use a scalar JSON value of at most 4096 characters.`)
  return value
}
export async function replayRequest(request, { url, identities, signal, variables = new Map() }) {
  try {
    signal?.throwIfAborted()
    const path = substitute(request.path, variables, true)
    const target = new URL(path, url)
    if (target.origin !== new URL(url).origin || !target.pathname.startsWith('/rest/v1/') || target.hash || target.username || target.password) throw new VariableFailure('Resolved request must use the local /rest/v1/ API.')
    const expected = 'json' in request.expect ? substitute(request.expect.json, variables) : undefined
    const body = 'body' in request ? substitute(request.body, variables) : undefined
    // Resolve inputs before clearing names, allowing a step to recapture a variable.
    for (const name of Object.keys(request.capture ?? {})) variables.delete(name)
    const response = await fetch(target, { method: request.method, ...('body' in request ? { body: JSON.stringify(body) } : {}), headers: { Authorization: `Bearer ${identities[request.identity].token}`, 'Content-Type': 'application/json', Prefer: 'return=representation' }, signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30000)]) : AbortSignal.timeout(30000), redirect: 'error' })
    // Bound response reads even when used independently of the gateway.
    const reader = response.body?.getReader(); const chunks = []; let size = 0
    try {
      if (reader) for (;;) {
        const { done, value } = await reader.read(); if (done) break
        size += value.length; if (size > 1048576) throw new Error('Response exceeds limit')
        chunks.push(value)
      }
    } finally { if (reader) { await reader.cancel().catch(() => {}); reader.releaseLock() } }
    const bytes = new Uint8Array(size); let offset = 0
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length }
    let json, validJSON = false
    try { json = JSON.parse(new TextDecoder().decode(bytes)); validJSON = true } catch {}
    const statusMatches = response.status === request.expect.status
    const jsonMatches = !('json' in request.expect) || (validJSON && matchesJSON(json, expected))
    const captured = []
    if (statusMatches && jsonMatches && Object.keys(request.capture ?? {}).length) {
      if (!validJSON) throw new VariableFailure('Cannot capture variables: response is not JSON.')
      const values = Object.entries(request.capture).map(([name, pointer]) => [name, captureValue(json, pointer, name)])
      for (const [name, value] of values) { variables.set(name, value); captured.push(name) }
    }
    return { ...(request.capture ? { captured } : {}), name: request.name, passed: statusMatches && jsonMatches, status: response.status, expected_status: request.expect.status, json_matches: jsonMatches }
  } catch (error) {
    for (const name of Object.keys(request.capture ?? {})) variables.delete(name)
    signal?.throwIfAborted()
    return { name: request.name, passed: false, error: error instanceof VariableFailure ? error.message : 'Request failed, timed out, or exceeded the response limit.' }
  }
}

export function collectionScenarios(collection, name = '') {
  const scenarios = collection.version === 1 ? [{ name: 'default', requests: collection.requests }] : collection.scenarios
  if (!name) return scenarios
  const selected = scenarios.find(scenario => scenario.name === name)
  if (!selected) throw new Error('Unknown replay scenario; select a name present in the collection.')
  return [selected]
}
