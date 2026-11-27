/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessIncidentEvent } from './MCPReadinessIncidentEvent.js';
/**
 * Paginated logical readiness events independent of notification delivery success.
 */
export type MCPReadinessIncidentTimeline = {
  events: Array<MCPReadinessIncidentEvent>;
  next_before?: string;
};

