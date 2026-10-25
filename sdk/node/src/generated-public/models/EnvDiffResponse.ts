/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvDiffRow } from './EnvDiffRow.js';
/**
 * Top-level response shape for GET /v1/apps/{slug}/env-diff
 * (ADR-117 PR-C). The matrix is always full (no `?scope=`
 * filter in v1). Rows are sorted ASC by key; scopes are
 * sorted ASC. Bounded payload: row count <= SecretCountMax +
 * EnvCountMax (200 on Scale), column count = customer's
 * scope set (1-3 typical).
 *
 */
export type EnvDiffResponse = {
  /**
   * Echoes the URL path parameter so the dashboard can render a header without re-parsing the URL.
   */
  app_slug: string;
  /**
   * Sorted ASC list of scopes present in the matrix. Consumers iterate this list for column ordering.
   */
  scopes: Array<string>;
  /**
   * Sorted ASC (by key) list of env-diff rows.
   */
  rows: Array<EnvDiffRow>;
  /**
   * RFC3339Nano timestamp the response was built. Dashboards use this to display stale badges.
   */
  generated_at: string;
};

