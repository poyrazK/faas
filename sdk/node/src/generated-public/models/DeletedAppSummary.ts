/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-owned retained lifecycle metadata. Eligibility never reserves quota or proves execution health.
 */
export type DeletedAppSummary = {
  account_id: string;
  id: string;
  slug: string;
  deleted_at: string | null;
  delete_grace_until: string | null;
  restore_eligible: boolean;
  restore_blocker: '' | 'grace_expired' | 'purge_started' | 'metadata_unavailable';
  evaluated_at: string;
};

