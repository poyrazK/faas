/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PeriodicProfilePolicy } from './PeriodicProfilePolicy.js';
import type { ProfileCanaryGatePolicy } from './ProfileCanaryGatePolicy.js';
import type { ProfileRegressionOptions } from './ProfileRegressionOptions.js';
/**
 * Opt-in background comparison policy. Collection must already be enabled on applications. Runtime filters apply to both deployments; capture coverage is not instrumentation completeness.
 */
export type ProfileDeploymentPolicyConfig = {
  /**
   * Omitted or null keeps canary checks advisory. Requires enabled checks and explicit routes.
   */
  canary_gate?: (ProfileCanaryGatePolicy | null);
  /**
   * Omitted or null disables periodic monitoring. Enabled automatic checks and explicit advisory routes are required.
   */
  periodic?: (PeriodicProfilePolicy | null);
  enabled: boolean;
  /**
   * Advisory app webhook notifications on evidence-qualified route regression and recovery transitions. Requires enabled checks and explicit options.routes. Applies to deployment and canary checks; does not affect rollout decisions.
   */
  notify_route_regressions?: boolean;
  runtime: string;
  window_seconds: number;
  warmup_seconds: number;
  options: ProfileRegressionOptions;
};

