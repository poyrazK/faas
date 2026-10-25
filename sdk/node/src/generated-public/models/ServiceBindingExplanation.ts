/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ServiceAuthorizationCheck } from './ServiceAuthorizationCheck.js';
import type { ServiceBindingTransport } from './ServiceBindingTransport.js';
/**
 * Separate-read policy and routing observation, not live identity, guest adoption or connectivity evidence.
 */
export type ServiceBindingExplanation = {
  caller: string;
  /**
   * Requested service name, which may differ from the resolved physical app slug.
   */
  target: string;
  method: string;
  /**
   * Decoded request path without query values.
   */
  path: string;
  result: 'allowed' | 'denied' | 'indeterminate';
  /**
   * Environment of the exact caller deployment.
   */
  scope: string;
  checked_at: string;
  /**
   * Observed app settings fingerprint; frozen environment policy may override these settings.
   */
  caller_fingerprint?: string;
  /**
   * Observed target app settings fingerprint; not a frozen release policy digest.
   */
  target_fingerprint?: string;
  caller_deployment_id?: string;
  target_app_id?: string;
  /**
   * Absent when the service route is unpinned or unavailable.
   */
  target_deployment_id?: string;
  /**
   * Resolved release, including retained project releases when permitted by the gateway.
   */
  release_id?: string;
  policy_source?: 'app' | 'environment_release';
  canonical_transport?: ServiceBindingTransport;
  checks: Array<ServiceAuthorizationCheck>;
  limitations: string;
};

