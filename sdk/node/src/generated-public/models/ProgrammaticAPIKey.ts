/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Freshly minted API key returned on the first
 * request. The `plaintext` field is the only copy the
 * caller will ever see — store it in `~/.config/faas/auth.json`
 * immediately. The `id` is the row's UUID for later
 * list/delete via `/v1/keys`.
 *
 */
export type ProgrammaticAPIKey = {
  /**
   * `fp_live_<48-hex>`. Returned ONCE.
   *
   */
  plaintext: string;
  /**
   * `fp_live_<8-hex>` — the prefix used in the auth
   * header for greppable identification.
   *
   */
  prefix: string;
  /**
   * API key row UUID.
   */
  id: string;
};

