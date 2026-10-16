/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ApplicationStandardDefinition } from './ApplicationStandardDefinition.js';
/**
 * One immutable canonical standard version and publishing provenance.
 */
export type ApplicationStandardVersion = {
  standard_id: string;
  org_id: string;
  slug: string;
  version: number;
  definition: ApplicationStandardDefinition;
  definition_hash: string;
  description: string;
  created_by: string;
  created_at: string;
};

