/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolInvocationIntent } from './ToolInvocationIntent.js';
/**
 * Private invocation intent plus bounded transient argument JSON. No credential value or secret reference is accepted.
 */
export type PrivateToolInvocationRequest = {
  grant_id: string;
  intent: ToolInvocationIntent;
  /**
   * Canonical JSON argument object matching the intent digest
   */
  arguments_json: string;
};

