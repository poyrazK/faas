import { businessDecisionPayload } from './customer-operation-decisions.js';
export interface OperationBusinessEffect {
 workflow: string; instance_id: string; state: string; operation: string; code: string; version: string;
 status: 'pending'|'failed'|'confirmed'; reference?: string; description: string; amount_minor?: number; currency?: string;
}
export interface OperationBusinessEffectPayload {kind: 'gregale.business-effect.v1'; effect: OperationBusinessEffect}
export function businessEffectPayload(input: OperationBusinessEffect): OperationBusinessEffectPayload {
 businessDecisionPayload({workflow:input.workflow,instance_id:input.instance_id,code:input.code,description:input.description,rule_id:input.state,rule_version:input.version});
 if(!/^[a-z][a-z0-9-]{0,63}$/.test(input.operation) || Buffer.byteLength(input.version)>64 || Buffer.byteLength(input.description)>256 || !['pending','failed','confirmed'].includes(input.status)) throw new TypeError('Invalid effect operation, version, description, or status');
 const reference=input.reference ?? '';
 if(typeof reference!=='string' || Buffer.byteLength(reference)>256 || Buffer.from(reference).toString('utf8')!==reference || /[\x00-\x1f\x7f]/.test(reference) || input.status==='confirmed' && !reference.trim()) throw new TypeError('Confirmed effects require a bounded reference without control characters');
 if(input.amount_minor===undefined ? !!input.currency : !Number.isSafeInteger(input.amount_minor) || input.amount_minor<0 || !/^[A-Z]{3}$/.test(input.currency ?? '')) throw new TypeError('Effect amount requires nonnegative safe minor units and uppercase currency');
 const effect={...input};
 if(effect.reference===undefined) delete effect.reference;
 if(effect.amount_minor===undefined) delete effect.amount_minor;
 if(effect.currency===undefined) delete effect.currency;
 return {kind:'gregale.business-effect.v1',effect};
}
