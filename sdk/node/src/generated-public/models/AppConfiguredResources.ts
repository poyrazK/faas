/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The memory and sustained CPU shape selected for each instance of this app.
 */
export type AppConfiguredResources = {
  /**
   * Configured instance memory in MB.
   */
  memory_mb: number;
  /**
   * Configured sustained CPU allowance in millicores.
   */
  cpu_millicores: 250 | 500 | 1000;
};

