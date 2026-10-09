/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationResultArtifact } from './OperationResultArtifact.js';
/**
 * Availability and metadata of one verified private HTTP invocation file receipt.
 */
export type OperationArtifactUploadResponse = {
  /**
   * True only for a verified private copy bound to this invocation attempt. Does not confirm business success.
   */
  available: boolean;
  artifact?: OperationResultArtifact;
};

