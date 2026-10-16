/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfilingConfig } from './ProfilingConfig.js';
import type { TracingConfig } from './TracingConfig.js';
/**
 * An atomic snapshot for journaling selected deployment policies before staging.
 */
export type DeploymentStagingSnapshot = {
  app_id: string;
  revision: number;
  /**
   * The latest deployment ID, or an empty string when no deployment exists.
   */
  latest_deployment_id: string;
  settings: {
    profiling?: (ProfilingConfig | null);
    tracing?: (TracingConfig | null);
  };
};

