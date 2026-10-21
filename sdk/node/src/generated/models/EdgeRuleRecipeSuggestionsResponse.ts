/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleRecipeSuggestion } from './EdgeRuleRecipeSuggestion.js';
/**
 * Protection recipes suggested from the app's traffic (ADR-20261020105000).
 */
export type EdgeRuleRecipeSuggestionsResponse = {
  /**
   * Telemetry window the suggestions read, in days: 7d, or the plan's telemetry retention when shorter.
   */
  window: string;
  /**
   * Start of the telemetry window.
   */
  since: string;
  /**
   * Ranked suggestions, at most five.
   */
  suggestions: Array<EdgeRuleRecipeSuggestion>;
};

