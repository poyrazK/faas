/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppResourceInventoryItem } from './AppResourceInventoryItem.js';
/**
 * One native app and scope metadata page with a filtered total and optional next offset.
 */
export type AppResourceInventoryResponse = {
  schema_version: 1;
  app_id: string;
  scope: string;
  project_id?: string;
  environment_id?: string;
  kind: 'domains' | 'crons' | 'buckets' | 'storage_bindings' | 'databases';
  limit: number;
  offset: number;
  total: number;
  items: Array<AppResourceInventoryItem>;
  has_more: boolean;
  next_offset?: number;
  generated_at: string;
};

