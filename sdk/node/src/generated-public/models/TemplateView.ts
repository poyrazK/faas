/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A starter template entry from GET /v1/templates (issue #961 / Mega-B PR-3).
 */
export type TemplateView = {
  /**
   * The template name — matches cmd/gregale/templates/embed.go::Names verbatim.
   */
  name: string;
  /**
   * Customer-facing group label (templates.CategoryFor).
   */
  category: 'hello' | 'function' | 'stateless-contract' | 'ai';
  /**
   * One-line customer-facing blurb.
   */
  description: string;
};

