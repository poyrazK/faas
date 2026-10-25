/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Pull-request preview provisioning payload. Source is deployed separately after the preview app is created.
 */
export type CreatePreviewRequest = {
  /**
   * Pull-request number used to derive the stable preview slug pr-{N}-{parent_slug}.
   */
  pr_number: number;
  /**
   * Preview lease duration. The server defaults to 168 hours and caps it at 30 days.
   */
  ttl_hours?: number;
};

