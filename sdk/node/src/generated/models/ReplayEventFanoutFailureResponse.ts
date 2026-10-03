/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One recipient requeued from its acceptance-time event snapshot.
 */
export type ReplayEventFanoutFailureResponse = {
  event_id: string;
  event_source: string;
  subscription_id: string;
  state: 'pending';
};

