/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One suggested protection recipe and why (ADR-20261020105000).
 */
export type EdgeRuleRecipeSuggestion = {
  /**
   * Recipe id to add (ADR-986).
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
};

