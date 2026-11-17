/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Closed producer disposition for a settled dispatch attempt. Failed includes application errors; retry does not distinguish a cause or establish a final result. Dead-letter classification remains separate. These are process-local attempts, not unique tasks or deliveries.
 */
export type ObsRuntimeDelayedTaskDispatchName = 'success' | 'retry' | 'failed' | 'dead_letter';
