/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request to expose one workload TCP port.
 */
export type CreateTCPListenerRequest = {
  name: string;
  guest_port: number;
  /**
   * Optional stable public port; Gregale allocates one when omitted.
   */
  public_port?: number;
};

