/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleRecipeSuggestionAlternative } from './EdgeRuleRecipeSuggestionAlternative.js';
/**
 * One suggested protection recipe and why.
 */
export type EdgeRuleRecipeSuggestion = {
  /**
   * Recipe id to add.
   */
  recipe: string;
  /**
   * The recipe's title.
   */
  title: string;
  /**
   * What in the app's traffic led to the suggestion.
   */
  reason: string;
  /**
   * Recipe parameters to add it with; required parameters not listed must be supplied.
   */
  params?: Record<string, string>;
  alternative?: EdgeRuleRecipeSuggestionAlternative;
};

