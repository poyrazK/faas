/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One app-owned raw TCP listener with a stable public endpoint.
 */
export type TCPListenerResponse = {
  /**
   * Stable listener identifier.
   */
  id: string;
  name: string;
  /**
   * TCP port exposed by the workload.
   */
  guest_port: number;
  /**
   * Stable Gregale public TCP port.
   */
  public_port: number;
  protocol: 'tcp';
  /**
   * Whether the edge accepts new TCP connections.
   */
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

