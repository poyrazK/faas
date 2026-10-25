/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A softer recipe for the same observation, such as challenging browsers instead of rate-limiting (ADR-831 amendment 7).
 */
export type EdgeRuleRecipeSuggestionAlternative = {
  /**
   * Recipe id of the alternative (ADR-986).
   */
  recipe: string;
  /**
   * The alternative recipe's title.
   */
  title: string;
  /**
   * Parameters to add the alternative with.
   */
  params?: Record<string, string>;
};

