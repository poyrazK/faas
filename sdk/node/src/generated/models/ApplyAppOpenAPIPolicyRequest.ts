/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Controls the explicit OpenAPI policy plan/apply workflow. Omit the
 * body (or set confirm=false) to request a read-only plan. A confirmed
 * apply must include the preview_sha256 returned by that plan.
 *
 */
export type ApplyAppOpenAPIPolicyRequest = {
  /**
   * Authorize creation of the generated validation rules.
   */
  confirm?: boolean;
  /**
   * Approval token returned by the current plan.
   */
  preview_sha256?: string;
  /**
   * Hostname for generated rules; defaults to the app hostname.
   */
  match_host?: string;
};

