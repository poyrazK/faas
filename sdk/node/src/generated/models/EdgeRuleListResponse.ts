/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A reusable account-level list (ADR-907).
 */
export type EdgeRuleListResponse = {
  id: string;
  name: string;
  kind: 'ip' | 'country' | 'host' | 'string';
  description?: string;
  item_count: number;
  /**
   * Present when one list is fetched.
   */
  items?: Array<string>;
  /**
   * IDs of rules whose match conditions use the list.
   */
  referenced_by: Array<string>;
  created_at: string;
  updated_at: string;
};

