/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Standard gRPC health Check RPC sent to the companion's loopback listener.
 */
export type SidecarGRPCProbe = {
  /**
   * gRPC container port; 0/omitted inherits the workload port.
   */
  port?: number;
  /**
   * Optional gRPC health service name; empty checks overall server health.
   */
  service?: string;
};

