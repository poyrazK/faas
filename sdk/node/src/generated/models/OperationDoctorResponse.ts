/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationDoctorCheck } from './OperationDoctorCheck.js';
/**
 * Read-only prerequisite observations for an owned deployment and tenant on one API node.
 */
export type OperationDoctorResponse = {
  app_id: string;
  scope: string;
  deployment_id: string;
  platform_tenant_id: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  observed_at: string;
  observation_scope: 'responding_api_node';
  /**
   * Submission prerequisites only. Delivery warnings and unverified qualification do not determine this observation.
   */
  submission_state: 'eligible' | 'blocked' | 'unknown';
  checks: Array<OperationDoctorCheck>;
};

