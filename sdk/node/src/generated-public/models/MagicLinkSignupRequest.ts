/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Email for the magic-link signup path. Optional; the
 * handler accepts a missing or unparseable email and still
 * returns 200 so the response cannot be used to enumerate
 * accounts.
 *
 */
export type MagicLinkSignupRequest = {
  email?: string;
};

