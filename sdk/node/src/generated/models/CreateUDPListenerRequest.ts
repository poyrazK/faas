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
  /**
   * Omit or set to zero for automatic allocation; otherwise reserve a port in the public UDP range.
   */
  public_port?: (0 | number);
};

