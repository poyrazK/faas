/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The clean live deployment to use when recovering an app from image-scan quarantine.
 */
export type SecurityQuarantineRecoveryRequest = {
  /**
   * Live replacement deployment with complete, digest-matched clean scan evidence.
   */
  deployment_id: string;
};

