/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A destination address and port a tenant guest opened a new flow to, or tried to and was blocked.
 */
export type EgressFlowLogEntry = {
  observed_at: string;
  node: string;
  account_id: string;
  app_id: string;
  instance_id: string;
  remote_ip: string;
  remote_port: number;
  protocol: 'tcp' | 'udp';
  /**
   * The egress policy dropped this attempt.
   */
  blocked: boolean;
};

