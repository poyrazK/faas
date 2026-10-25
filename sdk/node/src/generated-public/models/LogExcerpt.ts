/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One log entry attached to a `Problem` (error-explanations
 * cluster, spec §6.4 amendment 1) or persisted on
 * `deployments.error_relevant_logs`. The shape mirrors the
 * cluster's per-line log wire: `ts` is RFC3339 (apids
 * stamp format), `level` is one of `info|warn|error`,
 * `source` is the cluster's source discriminator
 * (build|vm-init|app|gateway), `message` is ≤512 bytes.
 *
 */
export type LogExcerpt = {
  ts?: string;
  level?: 'info' | 'warn' | 'error';
  source?: 'build' | 'vm-init' | 'app' | 'gateway';
  message?: string;
};

