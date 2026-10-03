/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sanitized span identity, label, status, and duration from retained request evidence.
 */
export type IssueHandoffSpan = {
  span_id: string;
  parent_span_id?: string;
  name: string;
  kind?: string;
  status?: string;
  duration_nanos: number;
};

