/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata for a prepared private copy or attached result reference, without download authority.
 */
export type OperationRecoveryArtifact = {
  id: string;
  name: string;
  workflow_step?: string;
  generation: number;
  attempt: number;
  state: 'prepared' | 'attached';
  /**
   * An owned retained storage receipt matches this reference at observation time; bytes are not fetched by inspection.
   */
  retained: boolean;
  size_bytes: number;
  sha256: string;
  expires_at: string;
};

