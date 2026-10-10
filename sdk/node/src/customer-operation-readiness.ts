import { OPERATION_WORKFLOW_STATE_BATCH_BYTES } from './operation-contract.js';
import { businessEffectPayload, type OperationBusinessEffect } from './customer-operation-effects.js';
import { businessInvariantPayload, type OperationBusinessInvariant } from './customer-operation-invariants.js';
import { businessDecisionPayload, type OperationBusinessDecision } from './customer-operation-decisions.js';
import type { CustomerOperationTransaction } from './customer-operation-milestones.js';
import type { CustomerOperationTransactionRequest } from './customer-operation-transactions.js';
import type { OperationWorkflowRetainedMilestoneRequirement, OperationWorkflowRetainedMilestone, OperationMilestoneReport, OperationWorkflowReadinessRequest, OperationWorkflowReadinessResponse } from './customer-operations.js';
import { canonicalWorkflowPaths, type OperationWorkflowStateReport } from './customer-operation-workflow-states.js';

function usableRetainedMilestone(e: OperationWorkflowRetainedMilestone): boolean {
 const f = e.freshness;
 return e.status === 'verified' && !!e.evidence && !e.invalidation && (!f || (Number.isSafeInteger(f.max_age_seconds) && f.max_age_seconds > 0 && !!f.evaluated_at && Number.isFinite(Date.parse(f.evaluated_at)) && !!f.expires_at && Number.isFinite(Date.parse(f.expires_at)) && Number.isSafeInteger(f.age_seconds) && f.age_seconds! >= 0 && Number.isSafeInteger(f.remaining_seconds) && f.remaining_seconds! > 0 && f.remaining_seconds! <= f.max_age_seconds));
}

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
    const request = {...proposal, selected_paths: canonicalWorkflowPaths(proposal.selected_paths ?? []), effects, invariants, decisions, subject: {...proposal.subject}, milestones: [...new Set(facts.map(fact => fact.name))]};
    for (const fact of facts) tx.milestone(fact.name, fact.payload);
    tx.workflowTransition(request.workflow, request.instance_id, request.from_state, request.to_state, request.selected_paths);
    const stagedReports = reports.slice(), stagedStates = states.slice();
    reports.splice(0, reports.length, ...priorReports); states.splice(0, states.length, ...priorStates);
    const response = await check({...request, decisions: request.decisions.map(p => ({...p,decision:{...p.decision}})), milestones: [...request.milestones], selected_paths: canonicalWorkflowPaths(request.selected_paths)});
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
    const usable = (e: OperationWorkflowRetainedMilestone) => usableRetainedMilestone(e) && (r.transition.retained_milestone_freshness ?? []).every(rule => rule.prerequisite.operation !== e.requirement.operation || rule.prerequisite.milestone !== e.requirement.milestone || (e.freshness?.max_age_seconds === rule.max_age_seconds && e.freshness.evaluated_at === response.evaluated_at));
    const evaluateFacts = (expected: OperationWorkflowRetainedMilestoneRequirement[], facts: OperationWorkflowRetainedMilestone[], all: boolean) => ({
      complete: expected.length > 0 && facts.length === expected.length && expected.every(p => facts.filter(e => e.requirement.operation === p.operation && e.requirement.milestone === p.milestone).length === 1),
      usable: all ? facts.every(usable) : facts.some(usable),
    });
    const groups = r.retained_milestone_groups ?? [], expectedGroups = r.transition.retained_milestone_groups ?? [];
    const selected = new Map(request.selected_paths.map(p => [p.group_id,p.path_id]));
    if (request.selected_paths.some(p => !expectedGroups.some(group => group.id === p.group_id && group.paths?.some(path => path.id === p.path_id)))) throw new TypeError('Selected path is not declared for this transition');
    for (const group of expectedGroups) { if (group.paths?.length && !selected.has(group.id)) throw new TypeError(`Guarded transitions require a selected path for compound group ${group.id}`); }
    if (groups.length !== expectedGroups.length || expectedGroups.some(expected => {
      const matches = groups.filter(group => group.id === expected.id);
      if (matches.length !== 1) return true;
      const group = matches[0];
      if (!group) return true;
      if (!group.satisfied || (group.selected_path_id ?? '') !== (selected.get(expected.id) ?? '')) return true;
      const choices = expected.any_of ?? [], paths = expected.paths ?? [], results = group.paths ?? [], alternatives = group.alternatives ?? [];
      if (!paths.length) {
        const result = evaluateFacts(choices, alternatives, false);
        return choices.length < 2 || results.length !== 0 || !result.complete || !result.usable;
      }
      if (choices.length || paths.length < 2 || alternatives.length || results.length !== paths.length) return true;
      let satisfied = false;
      for (const expectedPath of paths) {
        const matches = results.filter(path => path.id === expectedPath.id);
        const path = matches[0];
        if (matches.length !== 1 || !path) return true;
        const result = evaluateFacts(expectedPath.all_of, path.requirements, true);
        if (!expectedPath.decision) { if (path.decision) return true; }
        else {
          const rule = expectedPath.decision, d = path.decision;
          if (!d || (['milestone','code','rule_id','rule_version'] as const).some(key => d.requirement[key] !== rule[key])) return true;
          const planned = request.decisions.find(p => p.milestone === rule.milestone)?.decision;
          let matchesRule = !!planned && request.milestones.includes(rule.milestone) && planned.workflow === request.workflow && planned.instance_id === request.instance_id && planned.code === rule.code && planned.rule_id === rule.rule_id && planned.rule_version === rule.rule_version;
          const selection = request.selected_paths.find(p => p.group_id === expected.id && p.path_id === path.id);
          if (selection?.decision_milestone_id) matchesRule &&= selection.decision_code === rule.code && selection.decision_rule_id === rule.rule_id && selection.decision_rule_version === rule.rule_version;
          if (!!d.reported !== !!planned || planned && (['workflow','instance_id','code','description','rule_id','rule_version'] as const).some(key => d.reported?.[key] !== planned[key]) || d.satisfied !== matchesRule) return true;
          result.usable &&= matchesRule;
        }
        if (!result.complete || path.satisfied !== result.usable) return true;
        if (path.id === selected.get(expected.id)) satisfied ||= result.usable;
      }
      return !satisfied;
    })) throw new CustomerOperationReadinessError(response);
    if ((r.transition.required_retained_milestones ?? []).some(p => !(r.retained_milestones ?? []).some(e => e.requirement.operation === p.operation && e.requirement.milestone === p.milestone && usable(e))) || (r.retained_milestones ?? []).some(e => !usable(e)) || !r.ready || !r.declared || r.state_revision !== request.state_revision || r.contract_version !== request.contract_version || r.reasons.length + r.blockers.length + r.unmet_dependencies.length + r.missing_milestones.length + (r.missing_policies?.length ?? 0) + (r.missing_dependency_workflows?.length ?? 0) + (r.unmet_invariants?.length ?? 0) + (r.unmet_effects?.length ?? 0) !== 0) throw new CustomerOperationReadinessError(response);
    const linkedChoices = canonicalWorkflowPaths(request.selected_paths);
    for (const group of expectedGroups) for (const path of group.paths ?? []) {
      if (!path.decision) continue;
      const index = linkedChoices.findIndex(p => p.group_id === group.id && p.path_id === path.id);
      if (index < 0) continue;
      const matches = stagedReports.filter(f => f.name === path.decision!.milestone);
      if (matches.length !== 1) throw new TypeError('Path decision requires exactly one staged fact');
      const fact = matches[0]!, d = (fact.payload as {decision: OperationBusinessDecision}).decision;
      const linked = canonicalWorkflowPaths([{group_id:group.id,path_id:path.id,decision_milestone_id:fact.id,decision_code:d.code,decision_description:d.description,decision_rule_id:d.rule_id,decision_rule_version:d.rule_version}])[0]!;
      if (linkedChoices[index]!.decision_milestone_id && JSON.stringify(linkedChoices[index]) !== JSON.stringify(linked)) throw new TypeError('Path decision reference differs from staged evidence');
      linkedChoices[index] = linked;
    }
    stagedStates[stagedStates.length-1]!.selected_paths = linkedChoices;
    if (Buffer.byteLength(JSON.stringify({workflow_states:stagedStates})) > OPERATION_WORKFLOW_STATE_BATCH_BYTES) throw new TypeError('Workflow state batch exceeds its byte limit');
    reports.splice(0, reports.length, ...stagedReports); states.splice(0, states.length, ...stagedStates);
    return response;
  } catch (error) {
    fail(error);
    throw error;
  }
}
