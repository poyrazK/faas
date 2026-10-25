/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AccountAppSecretResponse } from './AccountAppSecretResponse.js';
/**
 * Page shape for `GET /v1/secrets` (issue #393). `secrets` is
 * the page in (app_slug ASC, key ASC) order. `next_before` is
 * the cursor the caller passes on the next request — encoded as
 * `<slug>|<key>`. Empty / null at the end.
 *
 */
export type ListSecretsForAccountResponse = {
  secrets: Array<AccountAppSecretResponse>;
  /**
   * Cursor in the form `<slug>|<key>`. Empty / null at the end.
   */
  next_before?: string | null;
};

