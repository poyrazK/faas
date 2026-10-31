/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentGRPCHealthcheck } from './DeploymentGRPCHealthcheck.js';
/**
 * Continuous primary-app readiness policy. It is evaluated only after
 * startup readiness succeeds. Once `failure_threshold` consecutive checks
 * fail, the running instance is removed from request routing. A successful
 * check restores routing; readiness failures never restart the VM.
 *
 * Defaults are period 5 seconds, timeout 2 seconds, and failure threshold
 * 3. Zero means use the corresponding default. Exactly one of `path`
 * (HTTP) and `grpc` (standard gRPC health.v1 Check) is required.
 *
 */
export type DeploymentReadinessProbe = {
  /**
   * HTTP path requested on the app's runtime port; must start with `/`. Set exactly one of path or grpc.
   */
  path?: string;
  /**
   * Use standard gRPC health.v1 Check. Set exactly one of path or grpc.
   */
  grpc?: DeploymentGRPCHealthcheck;
  /**
   * Probe interval in seconds; 0 = default (5).
   */
  period_s?: number;
  /**
   * Per-probe timeout in seconds; 0 = default (2).
   */
  timeout_s?: number;
  /**
   * Consecutive failures before traffic is withdrawn; 0 = default (3). A passing probe immediately restores routing.
   */
  failure_threshold?: number;
};

