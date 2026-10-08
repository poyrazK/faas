/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Idempotent app-reported state update already committed with the business write. Revision is assigned transactionally by the application SDK.
 */
export type OperationWorkflowStateReport = {
  id: string;
  workflow: string;
  instance_id: string;
  /**
   * Current app database state before the requested declared transition. Required when the pinned workflow declares transitions.
   */
  from_state?: string;
  state: string;
  revision: number;
  occurred_at: string;
};

