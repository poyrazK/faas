/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reserve a disabled public datagram endpoint for a declared workload port.
 */
export type CreateUDPListenerRequest = {
  name: string;
  guest_port: number;
  public_port?: number;
};

