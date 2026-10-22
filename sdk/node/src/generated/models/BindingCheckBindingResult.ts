/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingApplicationAdoption } from './BindingApplicationAdoption.js';
/**
 * Preflight status and safe evidence summary for one binding.
 */
export type BindingCheckBindingResult = {
  /**
   * Explicit scheduler readiness requirement; a passed policy result does not imply queue connectivity.
   */
  required_queue_evidence?: 'consumer_readiness' | 'handler_completion';
  /**
   * Exact admitted canary mode. Older evidence with no mode is listing-only and cannot satisfy write requirements.
   */
  object_storage_mode?: 'read' | 'write' | 'read_write';
  required_object_storage_mode?: 'read' | 'write' | 'read_write';
  type: string;
  name: string;
  binding?: string;
  scope: string;
  /**
   * passed, blocked, unsupported or skipped.
   */
  status: string;
  reason?: string;
  verification_status?: string;
  checked_at?: string;
  refresh_status?: string;
  application_adoption?: BindingApplicationAdoption;
};

