/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One live secret, identified by its key ID; key material is never returned.
 */
export type ManagedPostgresLiveKey = {
  key_id: string;
  active: boolean;
  created_at: string;
  /**
   * When a rotated-out secret stops opening seals.
   */
  retires_at?: string;
};

