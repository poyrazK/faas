/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App policy for dashboard publication. YAML deployment is a separate surface. Changes require admin scope and invalidate previously issued receipts.
 */
export type AutomationPublishPolicy = {
  mode: 'optional' | 'scenarios' | 'coverage';
  version: number;
};

