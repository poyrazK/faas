/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Listener TLS intent. Termination requires a verified app-owned ASCII DNS hostname; passthrough forbids a hostname.
 */
export type TCPListenerTLSConfig = {
  mode: 'passthrough' | 'terminate';
  /**
   * Normalized DNS hostname for termination. IP-shaped names and wildcards are forbidden.
   */
  hostname?: string;
};

