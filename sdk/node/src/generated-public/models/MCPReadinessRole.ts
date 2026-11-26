/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessMonitorConfig } from './MCPReadinessMonitorConfig.js';
/**
 * Role-specific protocol, catalog and monitoring requirements in a readiness policy.
 */
export type MCPReadinessRole = {
  monitor?: MCPReadinessMonitorConfig;
  name: string;
  protocol_version: '2026-07-28' | '2025-11-25';
  catalog_format: 1 | 2;
  catalog_sha256: string;
};

