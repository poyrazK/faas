/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The artifact hold for the exact deployment restored by an undo.
 */
export type ProjectRollbackUndoArtifacts = {
  deployment_id: string;
  /**
   * The deadline after a pause. Omitted while the operation holds the deployment for execution.
   */
  expires_at?: string;
};

