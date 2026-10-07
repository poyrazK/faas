/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationRevisionResponse } from './AutomationRevisionResponse.js';
/**
 * Newest-first immutable publication history and pagination metadata.
 */
export type ListAutomationRevisionsResponse = {
  revisions: Array<AutomationRevisionResponse>;
  total: number;
  limit: number;
  offset: number;
};

