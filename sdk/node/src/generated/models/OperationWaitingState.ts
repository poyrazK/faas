/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Why a waiting operation holds no execution. until is the timer's wake time or the approval deadline.
 */
export type OperationWaitingState = {
  kind: 'timer' | 'approval' | 'event';
  step: string;
  since: string;
  until?: string;
  approver?: 'account' | 'customer';
  /**
   * The event an event wait expects (ADR-1197).
   */
  event?: string;
};

