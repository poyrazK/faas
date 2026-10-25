/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TrafficRuntimeFeatureStatus } from './TrafficRuntimeFeatureStatus.js';
/**
 * Fresh compute gateway wiring observations for the active serving roster. Reports expire after 10 seconds; replacement, missing reports and readiness failure remove observations. Wiring does not prove backend health or request enforcement. Node identities and process tokens are not exposed.
 */
export type TrafficRuntimeStatus = {
  scope: 'compute_gateway_wiring';
  state: 'observed' | 'partial' | 'unverified';
  enforcement_status: 'unverified';
  serving_gateways: number;
  fresh_gateways: number;
  stale_gateways: number;
  missing_gateways: number;
  public_retry: TrafficRuntimeFeatureStatus;
  rate_counter: TrafficRuntimeFeatureStatus;
  retry_counter: TrafficRuntimeFeatureStatus;
  deadline_signing: TrafficRuntimeFeatureStatus;
  policy_snapshot: TrafficRuntimeFeatureStatus;
  security_revocation: TrafficRuntimeFeatureStatus;
  managed_http: TrafficRuntimeFeatureStatus;
  managed_circuit: TrafficRuntimeFeatureStatus;
};

