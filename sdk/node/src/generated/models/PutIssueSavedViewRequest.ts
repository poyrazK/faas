/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueSavedViewFilters } from './IssueSavedViewFilters.js';
/**
 * Shared issue-view display name and validated replacement filter set.
 */
export type PutIssueSavedViewRequest = {
  /**
   * Trimmed display name
   */
  name: string;
  filters: IssueSavedViewFilters;
};

