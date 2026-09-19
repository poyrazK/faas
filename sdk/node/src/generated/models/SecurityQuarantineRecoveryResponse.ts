/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The app's post-recovery active state.
 */
export type SecurityQuarantineRecoveryResponse = {
  app_id: string;
  slug: string;
  deployment_id: string;
  image_digest: string;
  recovered_at: string;
  status: 'active';
};

