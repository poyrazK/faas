/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Why one delivered action qualified, which projected fields changed since its last delivered snapshot, and advisory next steps. Missing historical snapshots never imply no changes; absence from a digest does not prove completion. Evidence comparisons describe references, not live evidence health or escalation state.
 */
export type OperationWorkflowNextActionDigestExplanation = {
  /**
   * Exact work-item identity, distinguishing multiple fulfillment reviews on one shared source.
   */
  obligation_id?: string;
  subscription_id: string;
  sequence: number;
  action_kind: string;
  reasons: Array<'overdue' | 'approaching_deadline' | 'waiting_for_acceptance' | 'waiting_for_fulfillment_review'>;
  comparison: 'first_delivery' | 'previous_delivery' | 'previous_snapshot_unavailable';
  changed_fields: Array<'obligation' | 'deadline' | 'overdue' | 'owner' | 'summary' | 'trigger' | 'evidence' | 'cases' | 'shared_plan' | 'scope' | 'subscription_tenant_id' | 'fulfillment_review'>;
  next_step: string;
};

