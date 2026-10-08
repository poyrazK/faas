/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One retained app-reported state update. Pages are ordered by revision, then stable publication and report identifiers.
 */
export type OperationWorkflowStateHistoryEntry = {
  id: string;
  operation_id: string;
  workflow: string;
  instance_id: string;
  /**
   * App state immediately before this retained revision
   */
  from_state?: string;
  state: string;
  revision: number;
  occurred_at: string;
  published_at: string;
  /**
   * Account-owner tenant identifier attached to this report in operator feeds.
   */
  platform_tenant_id?: string;
};

