/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One manual command to execute against the app's live deployment.
 * `command_shell=false` executes argv directly. Shell mode requires one
 * command string and is explicit so clients preserve quoting semantics.
 * `__gregale_service_binding_probe_v1__ <service>` is reserved for the
 * Gregale HTTPS service-binding canary and is handled by guest-init.
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

