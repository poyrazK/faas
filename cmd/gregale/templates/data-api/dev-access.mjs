import {validateMutations,runGuidedMutations} from './guided-mutations.mjs'
import {validateGuidedAccess,runGuidedAccess} from './guided-access.mjs'
import {validateRequests} from './dev-requests.mjs'
import { requestStore } from './dev-requests.mjs'
import { collectionScenarios, replayRequest } from './request-replay.mjs'
// This coverage check is intentionally limited to a local disposable verifier.
// It does not infer policy behavior from the presence of an expectation.
export function validateWriteQualification(manifest, project, scope) {
 const fail=()=>{throw new Error('Use a reviewed mutation manifest with allowed and cross-subject denied checks for each requested operation and fixture.')}
 if(!scope||scope.schema!=='api'||typeof scope.table!=='string'||!Array.isArray(scope.operations)||!scope.operations.length||new Set(scope.operations).size!==scope.operations.length||scope.operations.some(op=>!['insert','update','delete'].includes(op)))fail()
 if(manifest?.version!==4||manifest.status!=='reviewed')fail()
 validateMutations(manifest,Object.keys(project.identities))
 const fixtures=new Map([...manifest.fixtures,...manifest.mutationFixtures].map(item=>[item.name,item]))
 const subject=name=>project.identities[name]?.subject
 const methods={insert:'POST',update:'PATCH',delete:'DELETE'}
 const selected=new Set(scope.operations.map(op=>methods[op]))
 for(const check of manifest.mutations){
  const fixture=fixtures.get(check.fixture)
  if(!selected.has(check.method)||fixture?.schema!==scope.schema||fixture?.table!==scope.table||!subject(check.identity))fail()
 }
 const readSubjects=new Set(manifest.checks.filter(check=>check.steps.some((step,index)=>manifest.fixtures[index]?.schema===scope.schema&&manifest.fixtures[index]?.table===scope.table)).map(check=>subject(check.identity)).filter(Boolean))
 if(readSubjects.size<2)fail()
 for(const method of selected){
  const checks=manifest.mutations.filter(check=>check.method===method)
  const allowed=checks.filter(check=>check.expect.outcome!=='unchanged')
  const denied=checks.filter(check=>check.expect.outcome==='unchanged')
  if(!allowed.some(a=>readSubjects.has(subject(a.identity))&&denied.some(d=>readSubjects.has(subject(d.identity))&&a.fixture===d.fixture&&subject(a.identity)!==subject(d.identity))))fail()
 }
 return {schema:scope.schema,table:scope.table,operations:[...scope.operations],scope:'disposable_local_database',coverage:'supplied_scenarios_only',hosted_authorization:'not_tested'}
}
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
