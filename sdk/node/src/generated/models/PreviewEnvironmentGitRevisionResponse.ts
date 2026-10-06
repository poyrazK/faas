/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentDefinition } from './EnvironmentDefinition.js';
/**
 * Verified Git bytes with the digest and source generation needed for approval.
 */
export type PreviewEnvironmentGitRevisionResponse = {
  commit_sha: string;
  definition_digest: string;
  definition: EnvironmentDefinition;
  generation: number;
};

