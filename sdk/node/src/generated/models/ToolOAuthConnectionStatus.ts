/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Agent projection for one exact active invocation grant. Provider settings, secret references and credentials are excluded. Status is metadata, not proof of provider availability.
 */
export type ToolOAuthConnectionStatus = {
  grant_id: string;
  app: string;
  app_id: string;
  version: string;
  name: string;
  state: 'refresh_required' | 'ready' | 'refreshing' | 'reconnect_required';
  expires_at?: string;
  recovery_action?: 'reconnect' | 'review_connection';
};

