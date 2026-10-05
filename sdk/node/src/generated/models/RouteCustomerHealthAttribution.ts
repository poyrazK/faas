/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Weighted request attribution for one selected identity dimension across both shared windows. Missing identities and unresolved scoped identities remain separate. Other requests are identified traffic outside the cohort output cap.
 */
export type RouteCustomerHealthAttribution = {
  identified_requests: number;
  unattributed_requests: number;
  unresolved_identity_requests: number;
  other_customer_requests: number;
};

