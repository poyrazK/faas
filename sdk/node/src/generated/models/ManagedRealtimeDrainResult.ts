/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Outcome for one connection in a drain request.
 */
export type ManagedRealtimeDrainResult = {
  id: string;
  status: 'pending' | 'would_close' | 'closed' | 'gone' | 'failed';
};

