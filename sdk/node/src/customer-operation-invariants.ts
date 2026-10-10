import type { OperationWorkflowBlocker } from './customer-operation-workflow-states.js';
import { businessDecisionPayload } from './customer-operation-decisions.js';
export interface OperationBusinessInvariant {
 workflow: string; instance_id: string; state: string; code: string; version: string;
 status: 'passed'|'failed'|'unknown'; description: string; operations: string[];
}
export interface OperationBusinessInvariantPayload {kind: 'gregale.business-invariant.v1'; invariant: OperationBusinessInvariant}
export function businessInvariantPayload(input: OperationBusinessInvariant): OperationBusinessInvariantPayload {
 if(input.code.length>54 || Buffer.byteLength(input.version)>64 || Buffer.byteLength(input.description)>256 || !['passed','failed','unknown'].includes(input.status) || !Array.isArray(input.operations) || !input.operations.length || input.operations.length>16) throw new TypeError('Invalid invariant bounds or status');
 businessDecisionPayload({workflow:input.workflow,instance_id:input.instance_id,code:input.code,description:input.description,rule_id:input.state,rule_version:input.version});
 const operations=[...input.operations].sort();
 for(let i=0;i<operations.length;i++) if(typeof operations[i]!=='string' || !/^[a-z][a-z0-9-]{0,63}$/.test(operations[i]!) || i>0 && operations[i]===operations[i-1]) throw new TypeError('Invariant actions must be valid unique names');
 return {kind:'gregale.business-invariant.v1',invariant:{...input,operations}};
}
export function applyBusinessInvariant(input: OperationBusinessInvariant,current: OperationWorkflowBlocker[]): OperationWorkflowBlocker[] {
 const invariant=businessInvariantPayload(input).invariant,code='invariant-'+invariant.code;
 const blockers=current.filter(b=>b.code!==code).map(b=>({...b}));
 if(invariant.status!=='passed') for(const operation of invariant.operations){
  const blocker: OperationWorkflowBlocker={code,operation,description:`Invariant ${invariant.code} (${invariant.version}) ${invariant.status}: ${invariant.description}`};
  const prior=current.find(b=>b.code===code && b.operation===operation);
  if(prior?.first_observed_at) blocker.first_observed_at=prior.first_observed_at;
  blockers.push(blocker);
 }
 if(blockers.length>16) throw new TypeError('Invariant update exceeds workflow blocker limit');
 return blockers;
}
