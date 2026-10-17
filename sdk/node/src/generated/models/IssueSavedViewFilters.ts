/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Supported query filters retained by a shared application issue view.
 */
export type IssueSavedViewFilters = {
  /**
   * Omit to include every state.
   */
  state?: 'open' | 'resolved' | 'ignored';
  /**
   * Omit to include every issue environment.
   */
  environment?: string;
  /**
   * me, unassigned, or an eligible account UUID. Omit for all owners. me resolves for each authenticated viewer.
   */
  assignee?: string;
  sort?: 'recent' | 'impact';
  min_customers?: number;
  /**
   * Exact allowlisted operational key=value in retained occurrences over 24h; value is limited to 128 UTF-8 bytes.
   */
  tag?: string;
  /**
   * App-scoped team inbox; mutually exclusive with assignee.
   */
  team?: string;
};

