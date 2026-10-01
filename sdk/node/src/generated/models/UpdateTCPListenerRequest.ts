/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TCPListenerTLSConfig } from './TCPListenerTLSConfig.js';
/**
 * Supply exactly one serving-state or TLS-policy mutation. TLS changes disable the listener.
 */
export type UpdateTCPListenerRequest = {
  enabled?: boolean;
  tls?: TCPListenerTLSConfig;
};

