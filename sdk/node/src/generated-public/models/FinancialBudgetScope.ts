/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Authoritative account or resource identity; resource ids must belong to the account.
 */
export type FinancialBudgetScope = {
  kind: 'account' | 'project' | 'environment' | 'app' | 'job';
  /**
   * Omitted for account scope and required for resource scopes.
   */
  id?: string;
};

