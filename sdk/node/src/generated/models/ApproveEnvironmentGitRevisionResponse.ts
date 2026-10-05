/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentDesiredRevision } from './EnvironmentDesiredRevision.js';
import type { EnvironmentGitSource } from './EnvironmentGitSource.js';
/**
 * Durable approved revision and the updated management source.
 */
export type ApproveEnvironmentGitRevisionResponse = {
  source: EnvironmentGitSource;
  revision: EnvironmentDesiredRevision;
};

