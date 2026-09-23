/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One manual command to execute against the app's live deployment.
 * `command_shell=false` executes argv directly. Shell mode requires one
 * command string and is explicit so clients preserve quoting semantics.
 *
 */
export type CreateAppTaskRequest = {
  command: Array<string>;
  command_shell?: boolean;
  /**
   * Zero uses the 600-second default.
   */
  timeout_seconds?: number;
  /**
   * Zero uses the 1 MiB default; non-zero values must be at least 1024.
   */
  max_output_bytes?: number;
};

