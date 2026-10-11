/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Committed retained message mutation and resulting stream sequence.
 */
export type ManagedRealtimeMessageMutationResponse = {
  message_id: string;
  version: number;
  sequence: number;
  event: string;
  deleted: boolean;
};

