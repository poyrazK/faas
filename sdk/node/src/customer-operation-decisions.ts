export interface OperationBusinessDecision {
  workflow: string;
  instance_id: string;
  code: string;
  description: string;
  rule_id: string;
  rule_version: string;
}
export interface OperationBusinessDecisionPayload {
  kind: 'gregale.business-decision.v1';
  decision: OperationBusinessDecision;
}
export function businessDecisionPayload(decision: OperationBusinessDecision): OperationBusinessDecisionPayload {
  for (const [value, limit] of [[decision.workflow, 63], [decision.code, 64], [decision.rule_id, 64]] as const) {
    if (typeof value !== 'string' || value.length > limit || !/^[a-z][a-z0-9-]*$/.test(value)) throw new TypeError('Decision workflow, code, and rule ID must be bounded lowercase slugs');
  }
  for (const [value, limit] of [[decision.instance_id, 256], [decision.description, 1024], [decision.rule_version, 128]] as const) {
    if (typeof value !== 'string' || !value.trim() || Buffer.byteLength(value) > limit || Buffer.from(value).toString('utf8') !== value || /[\x00-\x1f\x7f]/.test(value)) throw new TypeError('Decision fields must be bounded nonempty text without control characters');
  }
  return {kind: 'gregale.business-decision.v1', decision: {...decision}};
}
