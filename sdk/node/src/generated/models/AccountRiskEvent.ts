/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An abuse event about the account, one of its apps, deployments or instances.
 */
export type AccountRiskEvent = {
  at: string;
  kind: string;
  actor: string;
  subject: string;
  data?: Record<string, any>;
};

