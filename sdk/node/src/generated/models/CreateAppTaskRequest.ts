/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One manual command to execute against the app's live deployment.
 * verification_deployment_id optionally selects an exact app-owned,
 * materialized live deployment for the reserved service, PostgreSQL,
 * object-storage or configured outbound verification probes only. Generic and smoke commands
 * cannot select a deployment. An explicit probe requires authorized,
 * managed binding metadata and remains live at atomic task admission.
 * The selector is supported only on direct POST /v1/apps/{slug}/tasks;
 * exclusive-operation task admission rejects it.
 * `command_shell=false` executes argv directly. Shell mode requires one
 * command string and is explicit so clients preserve quoting semantics.
 * `__gregale_service_binding_probe_v1__ <service>` is reserved for the
 * Gregale HTTPS service-binding canary and is handled by guest-init.
 * `__gregale_outbound_binding_probe_v1__ <integration-id>` selects a
 * configured outbound probe; the server supplies immutable gateway routing metadata.
 *
 */
export type CreateAppTaskRequest = {
  /**
   * Exact source deployment for a reserved binding verification probe; omit for the current manual-task selection.
   */
  verification_deployment_id?: string;
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

