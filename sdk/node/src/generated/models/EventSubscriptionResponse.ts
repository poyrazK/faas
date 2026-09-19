/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One manifest-declared event subscription reconciled for an app.
 */
export type EventSubscriptionResponse = {
  id: string;
  app_id: string;
  /**
   * Event source pattern, including supported wildcard forms.
   */
  source: string;
  /**
   * Event type pattern, including supported wildcard forms.
   */
  type: string;
  /**
   * Normalized JSON filter evaluated by the event matcher.
   */
  filter: Record<string, any>;
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

