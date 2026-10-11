import { businessEffectPayload, type OperationBusinessEffect } from './customer-operation-effects.js';
import { businessInvariantPayload, type OperationBusinessInvariant } from './customer-operation-invariants.js';
import { businessDecisionPayload, type OperationBusinessDecision } from './customer-operation-decisions.js';
import type { CustomerOperationTransaction } from './customer-operation-milestones.js';
import type { CustomerOperationTransactionRequest } from './customer-operation-transactions.js';
import type { OperationMilestoneReport, OperationWorkflowReadinessRequest, OperationWorkflowReadinessResponse } from './customer-operations.js';
import type { OperationWorkflowStateReport } from './customer-operation-workflow-states.js';

export class CustomerOperationReadinessError extends Error {
  constructor(readonly response: OperationWorkflowReadinessResponse) {
    super('Workflow transition readiness requirements are unmet');
    this.name = 'CustomerOperationReadinessError';
  }
}
export type CustomerOperationReadinessGuard = (
  proposal: OperationWorkflowReadinessRequest,
  facts: ReadonlyArray<{name: string; payload: unknown}>,
  check: (request: OperationWorkflowReadinessRequest) => Promise<OperationWorkflowReadinessResponse>,
) => Promise<OperationWorkflowReadinessResponse>;

// The application locks business rows and supplies their revision before calling.
export async function guardedWorkflowTransition(
  tx: CustomerOperationTransaction, owner: CustomerOperationTransactionRequest,
  reports: OperationMilestoneReport[], states: OperationWorkflowStateReport[],
  isOpen: () => boolean, fail: (error: unknown) => void,
  proposal: OperationWorkflowReadinessRequest, facts: ReadonlyArray<{name: string; payload: unknown}>,
  check: (request: OperationWorkflowReadinessRequest) => Promise<OperationWorkflowReadinessResponse>,
): Promise<OperationWorkflowReadinessResponse> {
  const priorReports = reports.slice(), priorStates = states.slice();
  try {
    if (!isOpen()) throw new TypeError('Readiness guards require an open callback');
    if (proposal.app_id !== owner.appId || proposal.tenant_id !== undefined || !Number.isSafeInteger(proposal.state_revision) || (proposal.state_revision ?? 0) <= 0 || !Number.isSafeInteger(proposal.contract_version) || (proposal.contract_version ?? 0) <= 0) throw new TypeError('Guard requires matching app and positive locked revision and contract version');
    const decisions = facts.flatMap(fact => {
      const payload = fact.payload as {kind?: string; decision?: OperationBusinessDecision} | null;
      if (payload?.kind !== 'gregale.business-decision.v1') return [];
      return [{milestone: fact.name, decision: businessDecisionPayload(payload.decision!).decision}];
    });
    const invariants=facts.flatMap(fact=>{
      const payload=fact.payload as {kind?: string; invariant?: OperationBusinessInvariant}|null;
      if(payload?.kind!=='gregale.business-invariant.v1') return [];
      return [{milestone:fact.name,invariant:businessInvariantPayload(payload.invariant!).invariant}];
    });
    const effects=facts.flatMap(fact=>{
      const payload=fact.payload as {kind?: string; effect?: OperationBusinessEffect}|null;
      if(payload?.kind!=='gregale.business-effect.v1')return [];
      return [{milestone:fact.name,effect:businessEffectPayload(payload.effect!).effect}];
    });
    const request = {...proposal, effects, invariants, decisions, subject: {...proposal.subject}, milestones: [...new Set(facts.map(fact => fact.name))]};
    for (const fact of facts) tx.milestone(fact.name, fact.payload);
    tx.workflowTransition(request.workflow, request.instance_id, request.from_state, request.to_state);
    const stagedReports = reports.slice(), stagedStates = states.slice();
    reports.splice(0, reports.length, ...priorReports); states.splice(0, states.length, ...priorStates);
    const response = await check(request);
    if (!isOpen()) throw new TypeError('Readiness guard must be awaited inside the callback');
    if (reports.length !== priorReports.length || states.length !== priorStates.length) throw new TypeError('Do not queue evidence concurrently with a readiness guard');
    const r = response.readiness;
    for (const pending of [...priorStates].reverse()) {
      if(pending.workflow!==request.workflow || pending.instance_id!==request.instance_id || !pending.blockers_only) continue;
      for(const blocker of pending.blockers ?? []) {
        if(blocker.operation!==request.operation || !blocker.code.startsWith('invariant-')) continue;
        if(!r.blockers.some(b=>b.code===blocker.code && b.operation===blocker.operation)) r.blockers.push(blocker);
        r.invariant_blockers ??= [];
        if(!r.invariant_blockers.some(b=>b.code===blocker.code && b.operation===blocker.operation)) r.invariant_blockers.push(blocker);
        if(!r.reasons.includes('application_blocked')) r.reasons.push('application_blocked');
        r.ready=false;
      }
      break;
    }
    if (response.subject.type !== request.subject.type || response.subject.id !== request.subject.id || response.workflow !== request.workflow || response.instance_id !== request.instance_id || r.transition.operation !== request.operation || r.transition.from !== request.from_state || r.transition.to !== request.to_state) throw new TypeError('Readiness response does not match proposed transition');
    if (!r.ready || !r.declared || r.state_revision !== request.state_revision || r.contract_version !== request.contract_version || r.reasons.length + r.blockers.length + r.unmet_dependencies.length + r.missing_milestones.length + (r.missing_policies?.length ?? 0) + (r.missing_dependency_workflows?.length ?? 0) + (r.unmet_invariants?.length ?? 0) + (r.unmet_effects?.length ?? 0) !== 0) throw new CustomerOperationReadinessError(response);
    reports.splice(0, reports.length, ...stagedReports); states.splice(0, states.length, ...stagedStates);
    return response;
  } catch (error) {
    fail(error);
    throw error;
  }
}
