/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessAttentionReceipt } from './MCPReadinessAttentionReceipt.js';
/**
 * Bounded immutable history of incident attention decisions.
 */
export type MCPReadinessAttentionHistory = {
  actions: Array<MCPReadinessAttentionReceipt>;
  next_before?: string;
};

