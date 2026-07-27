/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Delayed task create/get shape. ScheduledAt is the customer-facing UTC dispatch time. State is populated on get, omitted on create (always `pending` there).
 */
export type DelayedTaskResponse = {
  id: string;
  scheduled_at: string;
  state?: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled';
};

