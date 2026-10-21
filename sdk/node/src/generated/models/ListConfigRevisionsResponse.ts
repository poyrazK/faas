/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConfigGuard } from './ConfigGuard.js';
import type { ConfigRevision } from './ConfigRevision.js';
/**
 * A scope's config revisions, newest first, and its newest config guard (ADR-1451).
 */
export type ListConfigRevisionsResponse = {
  revisions: Array<ConfigRevision>;
  guard?: ConfigGuard;
};

