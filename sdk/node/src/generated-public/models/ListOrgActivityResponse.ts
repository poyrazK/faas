/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OrgActivityResponse } from './OrgActivityResponse.js';
/**
 * Newest-first global organization activity page.
 */
export type ListOrgActivityResponse = {
  items: Array<OrgActivityResponse>;
  /**
   * Continue from this value to retrieve older organization activity.
   */
  next_before?: string;
};

