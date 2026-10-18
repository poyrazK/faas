/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPCatalogMonitorConfig } from './MCPCatalogMonitorConfig.js';
import type { MCPCatalogOAuthLifecycle } from './MCPCatalogOAuthLifecycle.js';
import type { MCPCatalogObserverStatus } from './MCPCatalogObserverStatus.js';
import type { MCPCatalogReceipt } from './MCPCatalogReceipt.js';
/**
 * Configuration, latest observation and bounded transition history for a catalog monitor.
 */
export type MCPCatalogMonitorSnapshot = {
  oauth_lifecycle?: MCPCatalogOAuthLifecycle;
  observer_status?: MCPCatalogObserverStatus;
  /**
   * Additional role views when included; role-scoped reads omit this field. Use all_roles for aggregate state.
   */
  readonly roles?: Record<string, MCPCatalogMonitorSnapshot>;
  config: MCPCatalogMonitorConfig;
  checked_at?: string;
  latest?: MCPCatalogReceipt;
  history: Array<MCPCatalogReceipt>;
  state: 'disabled' | 'healthy' | 'drift' | 'unavailable';
};

