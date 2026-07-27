/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only instance view: id, deployment, state (waking/running/...), lease uid, and host-side internal endpoint (loopback only).
 */
export type InstanceResponse = {
  id: string;
  app_id: string;
  deployment_id: string;
  state: string;
  host_ip?: string | null;
  ram_mb: number;
  wake_id?: string;
  started_at?: string | null;
  last_request_at?: string | null;
  parked_at?: string | null;
};

