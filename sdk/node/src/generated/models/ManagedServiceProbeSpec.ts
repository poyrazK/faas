/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ServiceBindingTransport } from './ServiceBindingTransport.js';
/**
 * Server-selected frozen context for a managed service route probe. Contains no credentials or application request. Changing context or policy invalidates the receipt.
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
  /**
   * Caller transport the probe verifies. Absent means the original HTTPS probe; `http` probes the caller's plaintext `.svc.gregale` URL.
   */
  transport?: ServiceBindingTransport;
};

