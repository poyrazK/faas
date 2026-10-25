/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvDiffCell } from './EnvDiffCell.js';
import type { EnvDiffKind } from './EnvDiffKind.js';
/**
 * One (key, kind) row in the env-diff matrix. The Cells map is keyed by scope.
 */
export type EnvDiffRow = {
  key: string;
  kind: EnvDiffKind;
  /**
   * scope → cell. The handler populates the unioned set of scopes; consumers iterate EnvDiffResponse.Scopes for the canonical order.
   */
  cells: Record<string, EnvDiffCell>;
};

