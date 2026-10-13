/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained source receipt anchored to the saved obligation; observed_assignment describes the delivered assignment snapshot, not a newly audited write. Available event coverage is partial.
 */
export type OperationWorkflowObligationEvent = {
  /**
   * Exact obligation identity for mutation-time receipts; older inferred observations may omit it.
   */
  obligation_id?: string;
  kind: 'observed_assignment' | 'accepted' | 'declined' | 'reassigned' | 'completed' | 'canceled' | 'rescheduled' | 'assigned' | 'changed' | 'superseded';
  at: string;
  actor?: string;
  explanation: string;
};

