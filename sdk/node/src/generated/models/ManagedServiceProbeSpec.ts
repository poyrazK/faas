/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Server-selected frozen context for a managed HTTPS route probe. Contains no credentials or application request. Changing context or policy invalidates the receipt.
 */
export type ManagedServiceProbeSpec = {
  /**
   * Managed probe contract version; currently 1.
   */
  version: number;
  caller_deployment_id: string;
  scope: string;
  /**
   * Logical frozen service workload.
   */
  service: string;
  /**
   * Frozen environment key, including custom keys.
   */
  binding: string;
  target_app_id: string;
  target_deployment_id: string;
  release_id: string;
  context_revision: string;
};

