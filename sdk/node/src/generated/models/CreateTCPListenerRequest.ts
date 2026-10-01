/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TCPListenerTLSConfig } from './TCPListenerTLSConfig.js';
/**
 * Request to expose one workload TCP port. TLS termination listeners start disabled and require a verified app-owned hostname.
 */
export type CreateTCPListenerRequest = {
  name: string;
  guest_port: number;
  /**
   * Optional stable public port; Gregale allocates one when omitted.
   */
  public_port?: number;
  tls?: TCPListenerTLSConfig;
};

