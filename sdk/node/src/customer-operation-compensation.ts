import { businessEffectPayload } from './customer-operation-effects.js';
export interface OperationBusinessEffectReference {operation_id: string; milestone_id: string}
export interface OperationBusinessCompensation {
 workflow: string; instance_id: string; state: string; operation: string; code: string; version: string;
 status: 'required'|'pending'|'failed'|'confirmed'; source_effect: OperationBusinessEffectReference;
 reference?: string; description: string;
}
export interface OperationBusinessCompensationPayload {kind: 'gregale.business-compensation.v1'; compensation: OperationBusinessCompensation}
export function businessCompensationPayload(input: OperationBusinessCompensation): OperationBusinessCompensationPayload {
 if(!['required','pending','failed','confirmed'].includes(input.status)) throw new TypeError('Invalid compensation status');
 businessEffectPayload({workflow:input.workflow,instance_id:input.instance_id,state:input.state,operation:input.operation,code:input.code,version:input.version,status:input.status==='required'?'pending':input.status,description:input.description,...(input.reference!==undefined?{reference:input.reference}:{})});
 for(const id of [input.source_effect.operation_id,input.source_effect.milestone_id]) if(typeof id!=='string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id) || id==='00000000-0000-0000-0000-000000000000') throw new TypeError('Compensation source requires canonical nonzero UUIDs');
 const compensation={...input,source_effect:{...input.source_effect}};
 if(compensation.reference===undefined) delete compensation.reference;
 return {kind:'gregale.business-compensation.v1',compensation};
}
