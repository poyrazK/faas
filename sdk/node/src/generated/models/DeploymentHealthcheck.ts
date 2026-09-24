/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentGRPCHealthcheck } from './DeploymentGRPCHealthcheck.js';
/**
 * Readiness-probe shape on the deploy-time override object (issue #460 /
 * ADR-053). Exactly one of `path` (HTTP) or `grpc` (standard gRPC health
 * Check) selects the readiness action. The gRPC probe uses the app's
 * published port; an empty service checks overall server health.
 *
 * Validation rules (enforced in `pkg/api/dto.go::CreateDeploymentOverrides.Validate`):
 * - Exactly one of `path` and `grpc` must be set.
 * - `path`, when set, must start with `/`.
 * - `grpc.service` is optional and limited to 256 characters.
 * - `interval_s`, `timeout_s`, `retries` must be `>= 0`.
 * - Missing tuning fields default to 0; the host readiness deadline is
 * resolved separately from the app's plan and startup policy.
 *
 * OCI `test` argv and `start_period_s` remain deploy metadata; the host
 * readiness gate uses only the selected HTTP path or gRPC health RPC.
 *
 */
export type DeploymentHealthcheck = {
  /**
   * HTTP readiness path requested from the guest; must start with `/` (e.g. `/healthz`). Set exactly one of path or grpc.
   */
  path?: string;
  /**
   * Use the standard gRPC health.v1 Check RPC. Set exactly one of path or grpc.
   */
  grpc?: DeploymentGRPCHealthcheck;
  /**
   * Probe interval in seconds; 0 = use image default.
   */
  interval_s?: number;
  /**
   * Probe timeout in seconds; 0 = use image default.
   */
  timeout_s?: number;
  /**
   * Consecutive failures before the instance is considered unhealthy; 0 = use image default.
   */
  retries?: number;
  /**
   * Argv of the OCI HEALTHCHECK command, prefixed by "CMD", "CMD-SHELL", or "NONE". Surfaces onto AppManifest.Healthcheck.Test at apply_overrides time.
   */
  test?: Array<string>;
  /**
   * Startup grace during which probe failures don't count (Docker 17.05+, default 0s). Surfaces onto AppManifest.Healthcheck.StartPeriodS.
   */
  start_period_s?: number;
};

