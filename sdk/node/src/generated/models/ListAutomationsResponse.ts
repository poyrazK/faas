/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationResponse } from './AutomationResponse.js';
/**
 * App automation definitions, plan limit and execution availability.
 */
export type ListAutomationsResponse = {
  app_slug: string;
  runtime_enabled: boolean;
  unavailable_reason?: string;
  max_definitions: number;
  automations: Array<AutomationResponse>;
};

