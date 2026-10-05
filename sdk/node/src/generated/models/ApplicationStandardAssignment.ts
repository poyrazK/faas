/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained scope assignment and its current admission version, activity and concurrency revision.
 */
export type ApplicationStandardAssignment = {
  id: string;
  org_id: string;
  scope: 'organization' | 'project' | 'application';
  scope_id: string;
  standard_id: string;
  admission_version: number;
  revision: number;
  active: boolean;
  created_by: string;
  created_at: string;
  updated_at: string;
};

