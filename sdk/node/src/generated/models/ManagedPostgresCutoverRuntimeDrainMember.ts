/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe captured workload identity and retirement finding. Host identities and native proof details remain private.
 */
export type ManagedPostgresCutoverRuntimeDrainMember = {
  instance_id: string;
  deployment_id?: string;
  scope: string;
  mode: string;
  state: 'pending' | 'retired' | 'blocked';
  blocker_code?: string;
};

