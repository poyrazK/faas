/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationResultArtifact } from './OperationResultArtifact.js';
/**
 * A private verified workflow copy receipt; publication follows confirmed execution success.
 */
export type OperationWorkflowArtifactResponse = {
  /**
   * False only after successful authorization with no matching verified copy.
   */
  available: boolean;
  artifact?: OperationResultArtifact;
};

