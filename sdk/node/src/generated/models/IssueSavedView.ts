/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueSavedViewFilters } from './IssueSavedViewFilters.js';
/**
 * Named shared issue query with its stable key and creation and editor metadata.
 */
export type IssueSavedView = {
  key: string;
  name: string;
  filters: IssueSavedViewFilters;
  created_at: string;
  updated_at: string;
  updated_by_account_id?: string;
};

