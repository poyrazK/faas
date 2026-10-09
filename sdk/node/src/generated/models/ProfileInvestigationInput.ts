/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCallPath } from './ProfileCallPath.js';
import type { ProfileQuery } from './ProfileQuery.js';
/**
 * Saved comparison metadata. Title allows 160 UTF-8 bytes and findings and notes 8192 bytes each. Selections share a runtime. Changed windows must belong to retained deployments; unchanged expired windows permit commentary edits.
 */
export type ProfileInvestigationInput = {
  title: string;
  findings: string;
  notes: string;
  baseline: ProfileQuery;
  candidate: ProfileQuery;
  selected_path?: ProfileCallPath;
};

