/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Certificate evidence for one edge. Unknown evidence omits certificate expiry.
 */
export type TCPListenerTLSCertificateStatus = {
  edge_id: string;
  status: 'ready' | 'not_ready' | 'unknown';
  observed_at: string;
  not_after?: string;
};

