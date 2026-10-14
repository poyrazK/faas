/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CompanionDeclaration } from './CompanionDeclaration.js';
/**
 * Account-owned declarations for one exact deployment; metadata availability is separate from execution health.
 */
export type DeploymentCompanionsResponse = {
  account_id: string;
  app_id: string;
  app_slug: string;
  deployment_id: string;
  /**
   * Returned deployment scope; not inferred from a query parameter.
   */
  scope: string;
  /**
   * Declaration metadata availability only, never execution health.
   */
  state: 'available' | 'unavailable';
  reason?: 'declaration_metadata_unavailable';
  /**
   * Known empty is an available empty array. Unavailable metadata is explicitly labelled and must not be presented as no configured helpers.
   */
  companions: Array<CompanionDeclaration>;
};

