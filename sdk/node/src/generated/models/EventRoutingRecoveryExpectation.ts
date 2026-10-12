/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reviewed acceptance-time automation routing state. All comparisons occur in the replay transaction; mismatches return 409 event_recovery_changed without queuing a replay. Generation must be supplied for recipient routing and omitted for legacy routing. Does not bypass admission rules.
 */
export type EventRoutingRecoveryExpectation = {
  workflow_name: string;
  published_version: number;
  replay_count: number;
  generation?: number;
};

