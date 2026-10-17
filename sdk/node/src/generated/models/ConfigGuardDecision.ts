/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Why a config guard closed: the rule, a one-line summary and the new-config and old-config samples.
 */
export type ConfigGuardDecision = {
  rule: string;
  summary: string;
  new_requests: number;
  new_server_errors: number;
  old_requests: number;
  old_server_errors: number;
  restored_revision?: number;
  not_restorable?: Array<string>;
};

