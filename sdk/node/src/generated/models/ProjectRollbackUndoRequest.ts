/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ProjectRollbackUndoRequest = {
  /**
   * Acknowledge release commands crossed by this undo.
   */
  acknowledge_releases?: boolean;
  /**
   * Plan hash from a runnable undo preview of the same source with the same release acknowledgement.
   */
  expected_plan_hash?: string;
};

