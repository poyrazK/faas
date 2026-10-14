/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained catalog transition with server-computed drift, hashes and capture time.
 */
export type MCPCatalogReceipt = {
  role?: string;
  id: string;
  revision: string;
  captured_at: string;
  state: 'healthy' | 'drift' | 'unavailable';
  baseline_sha256: string;
  observed_sha256?: string;
  changed_count: number;
  diff?: Record<string, any>;
  truncated: boolean;
};

