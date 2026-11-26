/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessPagerDutyAcknowledgementEvent } from './MCPReadinessPagerDutyAcknowledgementEvent.js';
/**
 * Receipt page scoped to an owned readiness rule; next_before is absent on its final page.
 */
export type MCPReadinessPagerDutyAcknowledgementEvents = {
  events: Array<MCPReadinessPagerDutyAcknowledgementEvent>;
  next_before?: string;
};

