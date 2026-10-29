/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A runtime setting that differs; env overrides compare by key.
 */
export type ReleaseDiffField = {
  field: 'kind' | 'handler' | 'runtime_base' | 'port' | 'entrypoint' | 'command' | 'min_instances' | 'healthcheck' | 'liveness_probe' | 'readiness_probe' | 'sidecars' | 'workflows' | 'env_override_keys';
  from: string;
  to: string;
};

