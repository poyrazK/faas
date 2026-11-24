/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessEvidence } from './MCPReadinessEvidence.js';
import type { MCPReadinessMonitorStatus } from './MCPReadinessMonitorStatus.js';
import type { MCPReadinessOverride } from './MCPReadinessOverride.js';
import type { MCPReadinessPolicy } from './MCPReadinessPolicy.js';
/**
 * Environment readiness policy together with retained evidence and overrides.
 */
export type MCPReadinessSnapshot = {
  monitoring?: Array<MCPReadinessMonitorStatus>;
  policy: MCPReadinessPolicy;
  evidence: Array<MCPReadinessEvidence>;
  overrides: Array<MCPReadinessOverride>;
};

