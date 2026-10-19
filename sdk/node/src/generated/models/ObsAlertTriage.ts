/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Human workflow on one retained alert episode, independent of observed alert status.
 */
export type ObsAlertTriage = {
  revision: number;
  acknowledged: boolean;
  owned: boolean;
  owner_account_id?: string;
  snoozed_until?: string;
  snoozed: boolean;
  updated_at?: string;
  as_of: string;
};

