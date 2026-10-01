/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Receipt acknowledging an accepted or exact duplicate occurrence.
 */
export type IssueEventResponse = {
  issue_id: string;
  event_id: string;
  duplicate: boolean;
  regressed: boolean;
};

