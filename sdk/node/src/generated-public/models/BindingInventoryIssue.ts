/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable and sanitized reason why a binding inventory section could not be read.
 */
export type BindingInventoryIssue = {
  /**
   * Binding family or runtime_freshness or binding_refresh whose read was incomplete.
   */
  type: string;
  /**
   * Stable reason such as forbidden, unavailable, query_failed, consumer_status_unavailable or managed_postgres_unavailable.
   */
  code: string;
  severity: 'warning' | 'error';
  /**
   * Sanitized explanation without raw provider errors.
   */
  message: string;
};

