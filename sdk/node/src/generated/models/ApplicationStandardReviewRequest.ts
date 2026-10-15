/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit active and expected_revision are required, including false and zero. New assignments require active=true and revision=0.
 */
export type ApplicationStandardReviewRequest = {
  assignment_id?: string;
  scope: 'organization' | 'project' | 'application';
  scope_id: string;
  standard_id: string;
  admission_version: number;
  expected_revision: number;
  active: boolean;
  batch_size: number;
};

