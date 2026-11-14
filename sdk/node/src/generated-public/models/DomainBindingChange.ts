/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DomainBindingTarget } from './DomainBindingTarget.js';
/**
 * A proposed create, move or noop. New claims need verification. A move retains proof and TLS state.
 */
export type DomainBindingChange = {
  domain?: string;
  action?: 'create' | 'move' | 'noop';
  before?: DomainBindingTarget;
  after?: DomainBindingTarget;
  verified?: boolean;
  /**
   * The source app's default domain pointer will be cleared.
   */
  clears_default?: boolean;
  /**
   * Serving destination deployments captured for review; this is not an HTTP smoke-test result.
   */
  deployment_ids?: Array<string>;
};

