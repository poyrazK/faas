/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Call an existing customer managed outbound integration bound to this app.
 * Credentials and fixed-origin routing remain with outboundd. Input is a
 * templated JSON body; GET and HEAD have no body and forbid explicit input.
 * One provider call occurs per workflow attempt. Automatic mutating retries
 * require explicit provider idempotency support. Workflow outputs contain
 * status and body; sensitive headers and failed-response bodies are omitted.
 *
 */
export type WorkflowOutboundSpec = {
  /**
   * Canonical UUID of the customer managed integration bound to the app.
   */
  integration_id: string;
  /**
   * Provider HTTP method within both integration and app binding permissions.
   */
  method: 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  /**
   * Fixed canonical relative provider path; no URL, query, escaping, or traversal.
   */
  path: string;
  /**
   * Assert that the provider deduplicates the stable Idempotency-Key for this mutating operation; enables retries and recovery.
   */
  idempotency_supported?: boolean;
};

