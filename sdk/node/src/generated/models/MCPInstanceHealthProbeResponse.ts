/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded /healthz response from one exact replica with identity stamped by the platform.
 */
export type MCPInstanceHealthProbeResponse = {
  app_id: string;
  deployment_id: string;
  instance_id: string;
  /**
   * HTTP status returned by the workload's /healthz handler.
   */
  http_status: number;
  /**
   * UTF-8 body returned by the workload's /healthz handler.
   */
  health_body: string;
  /**
   * True when the workload response exceeded the 64 KiB probe limit and health_body contains only its bounded prefix.
   */
  health_body_too_large?: boolean;
};

