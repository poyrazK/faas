/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Debugger-only workflow action for one deployment/route observation.
 */
export type DebugRegressionActionRequest = {
  deployment_id: string;
  route: string;
  action: 'acknowledge' | 'dismiss' | 'resolve' | 'reopen';
  /**
   * Dismissal expiry; defaults to 24h and is capped at 30d.
   */
  dismissed_until?: string;
};

