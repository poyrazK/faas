/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Freshness-aware catalog health and evidence links used in monitoring notifications.
 */
export type MCPCatalogMonitorSignal = {
  state: 'disabled' | 'healthy' | 'drift' | 'unavailable';
  role?: string;
  checked_at?: string;
  receipt_id?: string;
  evidence_path?: string;
  changed_count: number;
  unavailable_roles?: Array<string>;
  drifted_roles?: Array<string>;
  roles?: Record<string, MCPCatalogMonitorSignal>;
};

