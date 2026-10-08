/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventSchemaRolloutConsumer = {
  subscription_id: string;
  app_id: string;
  /**
   * Subscription source pattern.
   */
  source: string;
  /**
   * Subscription type pattern.
   */
  type: string;
  /**
   * Empty accepts every version.
   */
  schema_versions: Array<string>;
  /**
   * Version selection only; no content filter evaluation or delivery guarantee.
   */
  accepts_version: boolean;
  content_filter_present: boolean;
};

