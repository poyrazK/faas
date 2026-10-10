/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body of `POST /v1/apps/{slug}/forks/{id}/execs`.
 */
export type CreateAppForkExecRequest = {
  /**
   * The argv to run. With `shell`, exactly one string run by the app's shell.
   */
  command: Array<string>;
  /**
   * Run the single command string through the app's shell.
   */
  shell?: boolean;
  /**
   * Default 60.
   */
  timeout_seconds?: number;
  /**
   * Combined stdout/stderr kept (the tail). Default 65536.
   */
  max_output_bytes?: number;
};

