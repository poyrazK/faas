/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained source receipt anchored to the saved obligation; observed_assignment describes the delivered assignment snapshot, not a newly audited write. Available event coverage is partial.
 */
export type OperationWorkflowObligationEvent = {
  kind: 'observed_assignment' | 'accepted' | 'declined' | 'reassigned' | 'completed' | 'canceled' | 'rescheduled';
  at: string;
  actor?: string;
  explanation: string;
};

