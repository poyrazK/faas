/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Egress contribution to a shared account compute and egress budget. Its funding allowance is pinned to the lowest recorded grant for the UTC month; increases benefit funding in a new month. Funded interface bytes include framing margin. Unused or uncertain credits commit conservatively after physical absence; invoice usage remains measured bytes.
 */
export type FinancialEgressBalance = {
  initialized: boolean;
  committed_bytes: number;
  reserved_bytes: number;
  allowance_granted_bytes: number;
  allowance_committed_bytes: number;
  allowance_reserved_bytes: number;
  allowance_remaining_bytes: number;
  committed_millicents: number;
  reserved_millicents: number;
};

