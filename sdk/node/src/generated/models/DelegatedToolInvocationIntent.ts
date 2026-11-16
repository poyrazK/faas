/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ToolInvocationIntent } from './ToolInvocationIntent.js';
/**
 * Exact owner-issued invocation grant plus explicit one-call intent. Only open access is accepted; credential_binding_sha256 must be omitted.
 */
export type DelegatedToolInvocationIntent = {
  grant_id: string;
  intent: ToolInvocationIntent;
};

