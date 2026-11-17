/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Closed durable task class. Restoring, retry_wait, held and unknown require all ages unknown. Running duration and held evidence do not establish an outage or bypass admission.
 */
export type ObsRuntimeAppTaskClassName = 'queued' | 'retry_wait' | 'held' | 'deadline_overdue' | 'restoring' | 'running' | 'cancellation_pending' | 'lease_expired' | 'unknown';
