/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentPromotionSummaryResponse } from './ProjectEnvironmentPromotionSummaryResponse.js';
/**
 * Cursor-paginated project environment promotion history.
 */
export type ProjectEnvironmentPromotionListResponse = {
  items: Array<ProjectEnvironmentPromotionSummaryResponse>;
  /**
   * Opaque cursor for the next older page.
   */
  next_before?: string;
};

