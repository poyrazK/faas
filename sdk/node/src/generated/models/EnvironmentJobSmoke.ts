/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reviewed argv-only command and short timeout for a job qualification attempt. The contract is frozen with the candidate; isolated execution and exit evidence are not yet available.
 */
export type EnvironmentJobSmoke = {
  /**
   * Executable and arguments passed directly without a shell.
   */
  command: Array<string>;
  /**
   * Explicit qualification wall-clock limit.
   */
  timeout_seconds: number;
};

