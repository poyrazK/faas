/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseSetResponse } from './ProjectReleaseSetResponse.js';
/**
 * Cursor-paginated release graphs, newest first, including expired sets.
 */
export type ProjectReleaseSetListResponse = {
  items: Array<ProjectReleaseSetResponse>;
  next_before?: string;
};

