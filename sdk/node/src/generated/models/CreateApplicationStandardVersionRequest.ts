/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardDefinition } from './ApplicationStandardDefinition.js';
/**
 * A strictly decoded candidate publication with an explicit concurrency version.
 */
export type CreateApplicationStandardVersionRequest = {
  expected_version: number;
  /**
   * UTF-8 description limited to 512 bytes.
   */
  description?: string;
  definition: ApplicationStandardDefinition;
};

