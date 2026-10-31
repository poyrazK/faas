/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A recognized blocker with a responsible actor and a safe next action. Unrecognized upstream errors are redacted.
 */
export type EnvironmentGitOpsDiagnostic = {
  resource?: string;
  stage: string;
  code: string;
  message: string;
  actor: 'customer' | 'operator' | 'controller';
  action: string;
};

