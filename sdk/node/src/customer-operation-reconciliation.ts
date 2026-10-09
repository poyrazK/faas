import type { OperationBusinessMilestoneOptions, OperationMilestones, OperationSubject } from './customer-operations.js';
import { businessDecisionPayload } from './customer-operation-decisions.js';
import type { CustomerOperationTransaction } from './customer-operation-milestones.js';
import type { OperationMilestoneReport } from './customer-operations.js';
import type { OperationWorkflowStateReport } from './customer-operation-workflow-states.js';
export interface OperationWorkflowReconciliationInput {
 workflow: string; instance_id: string; authoritative_state: string; source_revision: string;
 expected_report_revision: number; contract_version: number;
}
export interface OperationWorkflowReconciliation extends OperationWorkflowReconciliationInput {
 observed_state?: string; observed_report_revision: number; observed_contract_version: number;
 status: 'in_sync'|'report_missing'|'report_behind'|'report_ahead'|'state_mismatch'|'contract_version_mismatch';
}
export interface OperationWorkflowReconciliationPayload { kind: 'gregale.workflow-reconciliation.v1'; reconciliation: OperationWorkflowReconciliation }
export type CustomerOperationReconciliationHelper = (milestone: string, scope: string, subject: OperationSubject,
 input: OperationWorkflowReconciliationInput, read: (options: OperationBusinessMilestoneOptions) => Promise<OperationMilestones>) => Promise<OperationWorkflowReconciliation>;
export async function reconcileWorkflowState(tx: CustomerOperationTransaction, appID: string, reports: OperationMilestoneReport[], states: OperationWorkflowStateReport[], isOpen: () => boolean, fail: (error: unknown) => void,
 milestone: string, scope: string, subject: OperationSubject, input: OperationWorkflowReconciliationInput, read: (options: OperationBusinessMilestoneOptions) => Promise<OperationMilestones>): Promise<OperationWorkflowReconciliation> {
 try {
  if (!isOpen()) throw new TypeError('Reconciliation must be awaited inside the callback');
  businessDecisionPayload({workflow: input.workflow, instance_id: input.instance_id, code: input.authoritative_state, description: 'reconciliation', rule_id: 'reconciliation', rule_version: input.source_revision});
  if (!scope || !subject.type || !subject.id || !Number.isSafeInteger(input.expected_report_revision) || input.expected_report_revision < 0 || !Number.isInteger(input.contract_version) || input.contract_version < 1 || input.contract_version > 1000000) throw new TypeError('Invalid reconciliation reference or revision');
  const expected = {...input};
  const priorReports=reports.length, priorStates=states.length;
  const page=await read({appID,scope,subjectType:subject.type,subjectID:subject.id,workflow:expected.workflow,workflowInstanceID:expected.instance_id,limit:1});
  if (!isOpen() || reports.length!==priorReports || states.length!==priorStates) throw new TypeError('Await reconciliation sequentially inside the callback');
  const snapshot=page.workflow_instance;
  if (snapshot && (snapshot.workflow!==expected.workflow || snapshot.instance_id!==expected.instance_id)) throw new TypeError('Reconciliation snapshot identity differs');
  const state=snapshot?.state;
  if (state && (state.workflow!==expected.workflow || state.instance_id!==expected.instance_id || state.contract_version!==snapshot?.contract_version || !Number.isSafeInteger(state.revision) || state.revision < 1)) throw new TypeError('Invalid retained reconciliation state');
  const result: OperationWorkflowReconciliation={...expected,observed_report_revision:state?.revision ?? 0,observed_contract_version:snapshot?.contract_version ?? 0,status:'in_sync'};
  if(state) result.observed_state=state.state;
  if(result.observed_contract_version && result.observed_contract_version!==expected.contract_version) result.status='contract_version_mismatch';
  else if(!state) result.status='report_missing';
  else if(expected.expected_report_revision && state.revision>expected.expected_report_revision) result.status='report_ahead';
  else if(expected.expected_report_revision && state.revision<expected.expected_report_revision) result.status='report_behind';
  else if(state.state!==expected.authoritative_state) result.status='state_mismatch';
  if(result.status!=='in_sync') {
   if(['report_missing','report_behind','state_mismatch'].includes(result.status)) tx.workflowState(expected.workflow,expected.instance_id,expected.authoritative_state);
   tx.milestone(milestone,{kind:'gregale.workflow-reconciliation.v1',reconciliation:result});
   if(['report_missing','report_behind','state_mismatch'].includes(result.status)) {
    const fact=reports[reports.length-1]!;
    states[states.length-1]!.evidence_milestones=[{id:fact.id,name:fact.name}];
   }
  }
  return result;
 } catch(error) {fail(error);throw error;}
}
