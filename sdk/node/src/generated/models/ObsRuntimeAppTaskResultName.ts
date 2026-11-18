/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Ordered bounded outcome. Completion uses succeeded, failed, timed_out, cancelled or queued. Recovery uses requeued_restores, failed_runs or cancelled. A failed acknowledgement is not independently an infrastructure failure; failed_runs can have uncertain command outcomes.
 */
export type ObsRuntimeAppTaskResultName = 'succeeded' | 'failed' | 'timed_out' | 'cancelled' | 'queued' | 'requeued_restores' | 'failed_runs';
