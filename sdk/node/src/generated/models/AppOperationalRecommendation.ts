/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An actionable explanation of current operational evidence and an existing command or status endpoint for investigation.
 */
export type AppOperationalRecommendation = {
  code: string;
  severity: 'info' | 'warning' | 'error';
  message: string;
  next: string;
};

