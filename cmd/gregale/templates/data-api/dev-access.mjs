import {validateMutations,runGuidedMutations} from './guided-mutations.mjs'
import {validateGuidedAccess,runGuidedAccess} from './guided-access.mjs'
import {validateRequests} from './dev-requests.mjs'
import { requestStore } from './dev-requests.mjs'
import { collectionScenarios, replayRequest } from './request-replay.mjs'
export async function prepareAccessChecks(path, project) {
 const collection = await requestStore(path, { identities: Object.keys(project.identities),validate:value=>[4,5].includes(value?.version)?validateMutations(value,Object.keys(project.identities)):value?.version===3?validateGuidedAccess(value,Object.keys(project.identities)):validateRequests(value,{identities:Object.keys(project.identities)}) }).read(true)
 if([3,4,5].includes(collection.version)){const {revision,...manifest}=collection;return manifest}
 const scenarios = collectionScenarios(collection)
 if (!scenarios.length || scenarios.some(scenario => !scenario.requests.length)) throw new Error('Access scenarios must contain requests')
 for (const scenario of scenarios) for (const request of scenario.requests) {
  if (request.expect.status >= 200 && request.expect.status < 300 && !Object.hasOwn(request.expect, 'json')) throw new Error('Successful access assertions require explicit expected JSON')
 }
 return scenarios
}
export async function runAccessChecks(scenarios, context) {
 if([4,5].includes(scenarios.version))return runGuidedMutations(scenarios,context)
 if(scenarios.version===3)return runGuidedAccess(scenarios,context)
 const results = []
 for (const scenario of scenarios) {
  const variables = new Map(); let blocked = false
  for (const request of scenario.requests) {
   context.signal.throwIfAborted()
   const base = { scenario: scenario.name, name: request.name, identity: request.identity }
   if (blocked) { results.push({ ...base, passed: false, skipped: true, error: 'Earlier assertion failed; scenario stopped' }); continue }
   const result = await replayRequest(request, { ...context, variables, rejectContractMismatch: true })
   results.push({ ...base, passed: result.passed, ...(result.status ? { status: result.status, expected_status: result.expected_status, json_matches: result.json_matches } : {}), ...(!result.passed ? { error: 'Access assertion failed; inspect its expected status, JSON and configured identity' } : {}) })
   blocked = !result.passed
  }
 }
 return results
}
