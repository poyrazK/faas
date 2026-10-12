/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One app that appears on at least one service map edge.
 */
export type ServiceMapNode = {
  app_id: string;
  app_slug: string;
  /**
   * The app's opt-in to restore the services it calls while it wakes (ADR-977).
   */
  wake_ahead_enabled: boolean;
};

