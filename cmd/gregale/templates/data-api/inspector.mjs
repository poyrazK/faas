import { replayRequest, collectionScenarios } from './request-replay.mjs'
const element = id => document.getElementById(id)
const key = location.hash.slice(1)
history.replaceState(null, '', location.pathname)
let session, selected = '', fingerprint = '', busy = false
const show = (id, value) => { element(id).textContent = typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function selectResource() {
  selected = element('resource').value
  const [kind, name] = JSON.parse(selected)
  const resource = session.snapshot[kind].find(item => `${item.schema}.${item.name}` === name)
  show('schema', resource)
  element('path').value = `/rest/v1/${kind === 'functions' ? 'rpc/' : ''}${encodeURIComponent(resource.name)}${kind === 'tables' ? '?select=*' : ''}`
  element('method').value = kind === 'functions' ? 'POST' : 'GET'
  element('body').value = '{}'
}
element('resource').addEventListener('change', selectResource)
async function refresh() {
  try {
    const response = await fetch('session', { headers: { Authorization: `Bearer ${key}` }, cache: 'no-store' })
    if (!response.ok) throw new Error('Open the inspector URL printed by gregale data-api dev to connect.')
    session = await response.json()
    show('status', `${session.status} · API ${session.ready ? 'ready' : 'paused'} · client checks ${session.client_checked ? 'passed' : 'not verified'}`)
    show('detail', session.detail ?? '')
    show('fingerprint', `Schema: ${session.fingerprint}`)
    show('compatibility', session.compatibility ?? 'Initial local schema; no comparison yet.')
    if (fingerprint !== session.fingerprint) {
      fingerprint = session.fingerprint
      element('resource').replaceChildren()
      for (const kind of ['tables', 'functions']) for (const resource of session.snapshot[kind]) {
        const option = document.createElement('option')
        option.value = JSON.stringify([kind, `${resource.schema}.${resource.name}`]); option.textContent = `${kind === 'tables' ? 'Table' : 'RPC'} · ${resource.schema}.${resource.name}`
        element('resource').append(option)
      }
      if ([...element('resource').options].some(option => option.value === selected)) {
        element('resource').value = selected
        const [kind, name] = JSON.parse(selected)
        show('schema', session.snapshot[kind].find(item => `${item.schema}.${item.name}` === name))
      } else if (element('resource').value) selectResource()
    }
    element('send').disabled = !session.ready || busy
  } catch (error) { show('status', error.message); element('send').disabled = true }
}
element('send').addEventListener('click', async () => {
  busy = true; element('send').disabled = true
  try {
    const path = element('path').value
    const target = new URL(path, location.origin)
    if (target.origin !== location.origin || !target.pathname.startsWith('/rest/v1/') || target.username || target.password || target.hash) throw new Error('Use a local /rest/v1/ API path.')
    const method = element('method').value
    const body = ['POST', 'PATCH'].includes(method) ? JSON.stringify(JSON.parse(element('body').value)) : undefined
    const response = await fetch(target, { method, body, signal: AbortSignal.timeout(30000), headers: { Authorization: `Bearer ${session.identities[element('identity').value].token}`, 'Content-Type': 'application/json', Prefer: 'return=representation' } })
    show('result-status', `${response.status} ${response.statusText} · request ${response.headers.get('x-request-id') ?? '—'}`)
    const text = await response.text()
    try { show('response', JSON.parse(text)) } catch { show('response', text) }
  } catch (error) { show('result-status', error.message); show('response', '') }
  finally { busy = false; element('send').disabled = !session?.ready }
})

let collection
async function collectionRequest(method = 'GET', value) {
  const response = await fetch('requests', { method, headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json', ...(value ? { 'If-Match': collection.revision } : {}) }, ...(value ? { body: JSON.stringify(value) } : {}), signal: AbortSignal.timeout(15000) })
  const result = await response.json()
  if (!response.ok) throw new Error(result.error ?? 'Cannot load saved requests.')
  collection = result
  const selectedScenario = element('scenario').value
  element('scenario').replaceChildren()
  for (const scenario of collectionScenarios(collection)) {
    const option = document.createElement('option'); option.value = scenario.name; option.textContent = scenario.name; element('scenario').append(option)
  }
  if (collectionScenarios(collection).some(scenario => scenario.name === selectedScenario)) element('scenario').value = selectedScenario
  renderRequests()
  return result
}
const currentScenario = () => collection && collectionScenarios(collection).find(scenario => scenario.name === element('scenario').value)
function renderRequests() {
  const selected = element('saved').value
  element('saved').replaceChildren()
  for (const request of currentScenario()?.requests ?? []) {
    const option = document.createElement('option'); option.value = request.name; option.textContent = request.name; element('saved').append(option)
  }
  if (currentScenario()?.requests.some(request => request.name === selected)) element('saved').value = selected
}
const updatedCollection = requests => collection.version === 1 ? { version: 1, requests } : { version: 2, scenarios: collection.scenarios.map(scenario => scenario.name === element('scenario').value ? { ...scenario, requests } : scenario) }
element('scenario').addEventListener('change', renderRequests)
async function loadCollection() {
  try { await collectionRequest(); show('saved-status', 'Collection loaded.') }
  catch (error) { show('saved-status', error.message) }
}
const selectedRequest = () => currentScenario()?.requests.find(request => request.name === element('saved').value)
const savedAction = action => async () => {
  try { await action() } catch (error) { show('saved-status', error.message) }
}
element('refresh-saved').addEventListener('click', loadCollection)
element('load-saved').addEventListener('click', savedAction(async () => {
  const request = selectedRequest()
  if (!request) throw new Error('Select a saved request.')
  element('request-name').value = request.name; element('method').value = request.method; element('path').value = request.path; element('identity').value = request.identity
  element('body').value = JSON.stringify(request.body ?? {}, null, 2); element('expected-status').value = request.expect.status
  element('expected-json').value = 'json' in request.expect ? JSON.stringify(request.expect.json, null, 2) : ''
  element('capture').value = request.capture ? JSON.stringify(request.capture, null, 2) : ''
  show('saved-status', `Loaded ${request.name}.`)
}))
element('save-request').addEventListener('click', savedAction(async () => {
  if (!collection) throw new Error('Reload the collection first.')
  const method = element('method').value
  const request = { name: element('request-name').value.trim(), method, path: element('path').value, identity: element('identity').value, ...(element('capture').value.trim() ? { capture: JSON.parse(element('capture').value) } : {}), ...(['POST', 'PATCH'].includes(method) ? { body: JSON.parse(element('body').value) } : {}), expect: { status: Number(element('expected-status').value), ...(element('expected-json').value.trim() ? { json: JSON.parse(element('expected-json').value) } : {}) } }
  const requests = [...currentScenario().requests]; const index = requests.findIndex(item => item.name === request.name)
  if (index < 0) requests.push(request); else requests[index] = request
  await collectionRequest('PUT', updatedCollection(requests)); element('saved').value = request.name
  show('saved-status', `Saved ${request.name}.`)
}))
element('delete-saved').addEventListener('click', savedAction(async () => {
  const request = selectedRequest()
  if (!request) throw new Error('Select a saved request.')
  await collectionRequest('PUT', updatedCollection(currentScenario().requests.filter(item => item.name !== request.name)))
  show('saved-status', `Deleted ${request.name}.`)
}))
async function replay(scenarios) {
  if (!session?.ready || busy) throw new Error('Wait until the local API is ready.')
  if (!scenarios.some(scenario => scenario.requests.length)) throw new Error('Select or save a request first.')
  busy = true; element('send').disabled = true
  for (const id of ['replay', 'replay-all', 'replay-scenarios']) element(id).disabled = true
  const results = [], summaries = []
  show('scenario-results', summaries)
  try {
    for (const scenario of scenarios) {
      const variables = new Map()
      const start = results.length
      for (const request of scenario.requests) {
        results.push({ scenario: scenario.name, ...await replayRequest(request, { url: location.origin, identities: session.identities, variables }) })
        show('replay-results', results)
      }
      const passed = results.slice(start).filter(result => result.passed).length
      summaries.push({ scenario: scenario.name, total: scenario.requests.length, passed, failed: scenario.requests.length - passed })
      show('scenario-results', summaries)
    }
    show('saved-status', `${results.filter(result => result.passed).length}/${results.length} checks passed.`)
  } finally {
    busy = false; element('send').disabled = !session?.ready
    for (const id of ['replay', 'replay-all', 'replay-scenarios']) element(id).disabled = false
  }
}
element('replay').addEventListener('click', savedAction(() => replay([{ name: currentScenario()?.name, requests: selectedRequest() ? [selectedRequest()] : [] }])))
element('replay-all').addEventListener('click', savedAction(() => replay(currentScenario() ? [currentScenario()] : [])))

element('replay-scenarios').addEventListener('click', savedAction(() => replay(collection ? collectionScenarios(collection) : [])))
element('create-scenario').addEventListener('click', savedAction(async () => {
  if (!collection) throw new Error('Reload the collection first.')
  const name = element('scenario-name').value.trim()
  const scenarios = collectionScenarios(collection)
  if (!/^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(name) || scenarios.some(scenario => scenario.name === name)) throw new Error('Use a unique scenario identifier starting with a letter.')
  await collectionRequest('PUT', { version: 2, scenarios: [...scenarios, { name, requests: [] }] })
  element('scenario').value = name; renderRequests(); show('saved-status', `Created scenario ${name}.`)
}))
element('delete-scenario').addEventListener('click', savedAction(async () => {
  if (!collection) throw new Error('Reload the collection first.')
  const scenarios = collectionScenarios(collection), name = currentScenario().name
  if (scenarios.length === 1) throw new Error('Keep at least one scenario; delete its requests to empty it.')
  await collectionRequest('PUT', { version: 2, scenarios: scenarios.filter(scenario => scenario.name !== name) })
  show('saved-status', `Deleted scenario ${name}.`)
}))

await refresh()
await loadCollection()
setInterval(refresh, 1000)
