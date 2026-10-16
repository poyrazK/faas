/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfilingConfig } from './ProfilingConfig.js';
import type { TracingConfig } from './TracingConfig.js';
/**
 * The immutable non-secret intent used to stage or restore deployment policies.
 */
export type DeploymentStagingRequest = {
  session_id: string;
  operation: 'stage' | 'restore';
  expected_app_id: string;
  /**
   * The captured latest deployment ID, or an empty string when none exists.
   */
  expected_deployment_id: string;
  expected_revision: number;
  before: {
    profiling?: (ProfilingConfig | null);
    tracing?: (TracingConfig | null);
  };
  desired: {
    profiling?: (ProfilingConfig | null);
    tracing?: (TracingConfig | null);
  };
};

