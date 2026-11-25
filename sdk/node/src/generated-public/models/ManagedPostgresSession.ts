/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedPostgresSession = {
  pid: number;
  state: string;
  transaction_age_ms: number;
  wait_event_type?: string;
  wait_event?: string;
  application_name?: string;
  /**
   * Short-lived cancel target for this execution when available.
   */
  target_id?: string;
};

