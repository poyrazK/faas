/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RunArtifactObject } from './RunArtifactObject.js';
/**
 * Durable storage write receipt independent of source Run file retention and Agent thread lifetime.
 */
export type RunArtifactDelivery = {
  id: string;
  status: 'pending' | 'completed' | 'failed';
  size_bytes: number;
  content_type: string;
  error_code?: string;
  /**
   * Authenticated app-owned receipt locator
   */
  receipt_url: string;
  object?: RunArtifactObject;
  created_at: string;
};

