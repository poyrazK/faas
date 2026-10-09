/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Edit an edge-rule list: replace items, or add and remove some (ADR-907).
 */
export type UpdateEdgeRuleListRequest = {
  description?: string;
  /**
   * Replace every item. Cannot be combined with add or remove.
   */
  items?: Array<string>;
  add?: Array<string>;
  remove?: Array<string>;
};

