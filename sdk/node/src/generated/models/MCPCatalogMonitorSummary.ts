/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogMonitorSignal } from './MCPCatalogMonitorSignal.js';
import type { MCPCatalogMonitorSnapshot } from './MCPCatalogMonitorSnapshot.js';
/**
 * App health and individual catalog views for every configured role.
 */
export type MCPCatalogMonitorSummary = {
  state: 'disabled' | 'healthy' | 'drift' | 'unavailable';
  roles: Record<string, MCPCatalogMonitorSnapshot>;
  signal: MCPCatalogMonitorSignal;
};

