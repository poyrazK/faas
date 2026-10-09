/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One sanitized prerequisite, delivery configuration or unverified qualification observation.
 */
export type OperationDoctorCheck = {
  check: string;
  status: 'observed' | 'configured' | 'blocked' | 'warning' | 'unknown' | 'not_requested';
  impact: 'submission' | 'delivery' | 'qualification';
  /**
   * Stable non-secret reason code; filesystem paths and infrastructure errors are not exposed.
   */
  code: string;
  message: string;
  remediation?: string;
  definition_id?: string;
  name?: string;
  revision?: string;
  release_id?: string;
  /**
   * Immutable execution family for a definition-specific preview admission observation. Omitted for common prerequisites or an ambiguous contract.
   */
  execution_kind?: 'http' | 'workflow' | 'job';
  limit?: number;
  observed?: number;
};

