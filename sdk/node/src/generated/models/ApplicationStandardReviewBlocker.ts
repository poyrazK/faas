/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ApplicationStandardReviewBlocker = {
  app_id?: string;
  scope?: 'organization' | 'project' | 'application';
  scope_id?: string;
  field?: 'log_destinations' | 'require_signed' | 'security_policy' | 'trusted_publishers' | 'egress_cidrs' | 'egress_extra_ports';
  code: string;
};

