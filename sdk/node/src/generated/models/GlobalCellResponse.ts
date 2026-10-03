/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable registered cell identity with mutable HTTPS origin and enabled policy. Generation fences verification.
 */
export type GlobalCellResponse = {
  id: string;
  region: string;
  /**
   * Canonical HTTPS origin without credentials or path.
   */
  api_base: string;
  enabled: boolean;
  generation: number;
};

