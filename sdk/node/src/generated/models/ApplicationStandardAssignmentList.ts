/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardAssignment } from './ApplicationStandardAssignment.js';
/**
 * Page of retained assignments with an optional exclusive cursor for the next page.
 */
export type ApplicationStandardAssignmentList = {
  assignments: Array<ApplicationStandardAssignment>;
  next_page_after?: string;
};

