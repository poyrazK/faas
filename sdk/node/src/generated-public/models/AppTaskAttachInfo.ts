/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One-time attach credential for an interactive app task. Returned only in the create response.
 */
export type AppTaskAttachInfo = {
  /**
   * Send in X-Gregale-Attach-Token with the account credential; consumed by the first attach.
   */
  token: string;
  /**
   * Gateway WebSocket path for this task.
   */
  path: string;
  subprotocol: 'gregale-app-task-attach-v1';
};

