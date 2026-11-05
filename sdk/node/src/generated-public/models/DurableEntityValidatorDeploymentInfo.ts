/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observational validator readiness on deployment detail; not a reservation or code-purity attestation.
 */
export type DurableEntityValidatorDeploymentInfo = {
  status: 'disabled' | 'unavailable' | 'ready';
  source?: 'registry' | 'object_storage';
  sha256?: string;
};

