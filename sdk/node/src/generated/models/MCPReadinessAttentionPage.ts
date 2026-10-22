/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessIncidentAttention } from './MCPReadinessIncidentAttention.js';
/**
 * Bounded page of readiness incident attention ordered by incident UUID ascending.
 */
export type MCPReadinessAttentionPage = {
  incidents: Array<MCPReadinessIncidentAttention>;
  next_after?: string;
};

