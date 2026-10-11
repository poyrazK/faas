/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One manual command to execute against the app's live deployment.
 * verification_deployment_id optionally selects an exact app-owned,
 * materialized live deployment for the reserved service, PostgreSQL,
 * object-storage or configured outbound verification probes only.
 * smoke_deployment_id selects an exact app-owned, materialized live caller
 * deployment for the reserved service smoke GET command. The service must
 * be declared on the caller; target authorization remains enforced by the gateway.
 * Generic commands cannot select a deployment. The selectors are mutually
 * exclusive and require the selected deployment to remain live at atomic
 * task admission. An explicit verification probe also requires authorized,
 * managed binding metadata. Both selectors are supported only on direct
 * POST /v1/apps/{slug}/tasks; exclusive-operation task admission rejects them.
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
  /**
   * Exact caller deployment for a reserved service smoke GET; mutually exclusive with verification_deployment_id. Omit for automatic caller selection.
   */
  smoke_deployment_id?: string;
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

