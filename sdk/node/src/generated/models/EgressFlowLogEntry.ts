/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A destination address and TCP port a tenant guest opened a new flow to (ADR-369).
 */
export type EgressFlowLogEntry = {
  observed_at: string;
  node: string;
  account_id: string;
  app_id: string;
  instance_id: string;
  remote_ip: string;
  remote_port: number;
};

