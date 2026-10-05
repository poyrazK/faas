/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingApplicationAdoption } from './BindingApplicationAdoption.js';
/**
 * Preflight status and safe evidence summary for one binding.
 */
export type BindingCheckBindingResult = {
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

