/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable directory tenant root. Account roots retain the account UUID; organization roots have an independent UUID and survive creator departure.
 */
export type GlobalAppOwnerResponse = {
  id: string;
  kind: 'account' | 'organization';
  /**
   * Present only for account ownership.
   */
  account_id?: string;
  /**
   * Present only for organization ownership.
   */
  org_id?: string;
};

