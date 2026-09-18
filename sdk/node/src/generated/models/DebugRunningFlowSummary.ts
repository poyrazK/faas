/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A bounded endpoint-level flow summary observed for a running
 * instance. The platform omits payloads, headers, and unbounded
 * connection data.
 *
 */
export type DebugRunningFlowSummary = {
  instance_id?: string;
  protocol?: string;
  remote_ip?: string;
  remote_port?: number;
  state?: string;
  direction?: string;
  count?: number;
};

