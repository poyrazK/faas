/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationResultArtifact } from './OperationResultArtifact.js';
/**
 * Availability and metadata of one verified private native Job file receipt.
 */
export type OperationJobArtifactResponse = {
  /**
   * True only for a verified private copy bound to this generation. Does not confirm business success.
   */
  available: boolean;
  artifact?: OperationResultArtifact;
};

