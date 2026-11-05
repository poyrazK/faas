/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stored RAM, CPU, concurrency and instance-floor settings.
 */
export type AppScalingSettings = {
  ram_mb: number;
  /**
   * Stored CPU allowance; zero uses the plan default.
   */
  cpu_millicores: number;
  max_concurrency: number;
  min_instances: number;
};

