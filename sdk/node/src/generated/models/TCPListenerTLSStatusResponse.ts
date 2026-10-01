/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TCPListenerTLSCertificateStatus } from './TCPListenerTLSCertificateStatus.js';
import type { TCPListenerTLSConfig } from './TCPListenerTLSConfig.js';
/**
 * Certificate evidence from observed edges; empty observations mean unknown status, never fleet-wide readiness.
 */
export type TCPListenerTLSStatusResponse = {
  name: string;
  tls: TCPListenerTLSConfig;
  enabled: boolean;
  scope: 'observed_edges';
  observations: Array<TCPListenerTLSCertificateStatus>;
};

