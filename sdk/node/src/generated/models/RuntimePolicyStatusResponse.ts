/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RuntimePolicyComponentStatus } from './RuntimePolicyComponentStatus.js';
import type { RuntimePolicyNodeStatus } from './RuntimePolicyNodeStatus.js';
import type { RuntimePolicySchedulerStatus } from './RuntimePolicySchedulerStatus.js';
import type { TrafficRuntimeStatus } from './TrafficRuntimeStatus.js';
/**
 * Runtime policy status across gateway replicas, the owning scheduler, and live VM consumers. Each component reports its scoped desired revision. Gateway request policy filters app-row changes from the combined app-cache and traffic projection. Gateway acknowledgements require the current process generation and a fresh serving report. Traffic runtime reports wiring independently of convergence and enforcement; older API servers may omit it.
 */
export type RuntimePolicyStatusResponse = {
  app_id: string;
  /**
   * Latest desired control-plane revision for app-cache and deployment traffic policy.
   */
  desired_revision: number;
  state: 'active' | 'pending' | 'unverified';
  coverage: Array<string>;
  serving_gateways: number;
  applied_gateways: number;
  pending_gateways: number;
  stale_gateways: number;
  request_policy: RuntimePolicyComponentStatus;
  edge_rules: RuntimePolicyComponentStatus;
  cors_presets: RuntimePolicyComponentStatus;
  response_cache: RuntimePolicyComponentStatus;
  egress_allowlist: RuntimePolicyNodeStatus;
  cpu_limit: RuntimePolicyNodeStatus;
  scheduler_scaling: RuntimePolicySchedulerStatus;
  traffic_runtime?: TrafficRuntimeStatus;
};

