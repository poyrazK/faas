/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable registered cell identity with mutable management origin, regional apps domain and enabled policy. Generation fences verification.
 */
export type GlobalCellResponse = {
  id: string;
  region: string;
  /**
   * Canonical HTTPS origin without credentials or path.
   */
  api_base: string;
  /**
   * Canonical regional apps DNS suffix; omitted when unconfigured. No port, scheme, literal IP or trailing dot.
   */
  apps_domain?: string;
  enabled: boolean;
  generation: number;
};

