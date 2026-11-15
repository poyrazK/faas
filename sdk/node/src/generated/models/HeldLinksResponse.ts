/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HeldLinkReview } from './HeldLinkReview.js';
/**
 * Recent held-account links, newest first. truncated means the page limit was reached.
 */
export type HeldLinksResponse = {
  links: Array<HeldLinkReview>;
  truncated: boolean;
};

