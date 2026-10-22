/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete role catalog or an unavailable observation bound to its reviewed revision.
 */
export type MCPCatalogObservationRequest = {
  role?: string;
  revision: string;
  unavailable: boolean;
  /**
   * Complete observed format-2 contract; omit when unavailable is true
   */
  observed?: Record<string, any>;
};

