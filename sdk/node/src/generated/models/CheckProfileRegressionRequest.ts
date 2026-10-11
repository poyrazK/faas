/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileRegressionOptions } from './ProfileRegressionOptions.js';
/**
 * Revision-protected on-demand assessment request. Omitted options use CPU/s, 20 percent, 0.01 CPU/s, three profiles and 0.8 capture ratio. CPU/request mode requires both request-specific options.
 */
export type CheckProfileRegressionRequest = {
  expected_revision: number;
  options?: ProfileRegressionOptions;
};

