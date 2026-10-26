/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProgrammaticAPIKey } from './ProgrammaticAPIKey.js';
/**
 * Body for the JSON-only POST /v1/auth/{signup,login} pair
 * (issue #311). Distinct from PasswordLoginResponse: this
 * one carries the `api_key` payload so the bearer-key CLI
 * can use the result without a dashboard round-trip. The
 * plaintext is returned ONCE; the caller persists it via
 * `saveToken()` before this response is dropped.
 *
 * Email is echoed back so the CLI's finalizeLogin step can
 * render "Logged in as <email> (<plan> plan)" without an
 * extra Whoami round-trip.
 *
 */
export type ProgrammaticAuthResponse = {
  /**
   * Account UUID.
   */
  account_id: string;
  /**
   * Email the client sent in the POST body. Echoed back so the CLI can render the success line without a Whoami round-trip.
   */
  email: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  api_key: ProgrammaticAPIKey;
};

