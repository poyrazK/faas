/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountRiskLink } from './AccountRiskLink.js';
/**
 * A held-account link with the new account's sign-up country and email domain.
 */
export type HeldLinkReview = (AccountRiskLink & {
  country?: string;
  email_domain?: string;
  disposable_email: boolean;
});

