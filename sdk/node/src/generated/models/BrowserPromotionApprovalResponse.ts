/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Five-minute human review link with separate private polling material. Only review_url and expires_at may be exposed to agents.
 */
export type BrowserPromotionApprovalResponse = {
  review_url: string;
  ticket: string;
  exchange_code: string;
  expires_at: string;
};

