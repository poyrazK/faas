/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentDefinition } from './EnvironmentDefinition.js';
/**
 * Immutable approved definition retained for reconciliation during Git outages.
 */
export type EnvironmentDesiredRevision = {
  id: string;
  source_id: string;
  commit_sha: string;
  definition_digest: string;
  definition: EnvironmentDefinition;
  approved_by: string;
  approved_at: string;
};

