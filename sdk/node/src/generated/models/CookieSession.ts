/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Dashboard session cookie. Sealed; opaque to the client
 * (`HttpOnly; Secure; SameSite=Lax`). 7-day fixed lifetime.
 * The browser sets it automatically on `/login` / `/signup`;
 * the SDK uses the device-code flow instead and never sets
 * this cookie.
 *
 */
export type CookieSession = string;
