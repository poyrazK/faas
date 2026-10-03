/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One-time artifact bearer capability. The token is returned once; the server stores only its hash.
 */
export type ExecutionArtifactGrantResponse = {
  id: string;
  source_execution_id: string;
  artifact_name: string;
  /**
   * Bearer capability returned once; the server stores only its hash.
   */
  readonly token: string;
  expires_at: string;
};

