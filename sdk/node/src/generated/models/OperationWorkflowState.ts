/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Latest app-reported state for one declared workflow instance, including terminal and staleness indicators.
 */
export type OperationWorkflowState = {
  workflow: string;
  instance_id: string;
  state: string;
  /**
   * True when the state is listed in terminal_states on the pinned workflow definition that reported it.
   */
  terminal: boolean;
  /**
   * True when the app-reported occurrence time plus the pinned state_stale_after threshold is at or before the read time.
   */
  stale: boolean;
  /**
   * App-reported time when the current state became true; used for stale-state age.
   */
  occurred_at: string;
  /**
   * App-declared age threshold for the current state when one is configured.
   */
  stale_after_seconds?: number;
  revision: number;
  updated_at: string;
  /**
   * Included only for account operator feeds.
   */
  platform_tenant_id?: string;
};

