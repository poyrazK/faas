/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Resolved immutable contract for an HTTP handler or a named linear HTTP workflow from the same deployment. Workflow definitions require POST ingress, reconciliation recovery and stages matching the steps. Ownership comes from verified authentication. Production admission remains disabled pending qualification.
 */
export type OperationDefinitionSpec = {
  name: string;
  /**
   * Optional account-owned active batch Job. POST ingress and reconciliation recovery only; one task per generation. Input, image, command, environment and execution policy are frozen at admission. Mutually exclusive with workflow and transaction_receipt.
   */
  job?: string;
  /**
   * Optional named workflow captured from this immutable deployment; path becomes its submission route.
   */
  workflow?: string;
  /**
   * Explicit HTTP/PostgreSQL receipt adapter; requires reconciliation recovery. Business writes and the saved result commit in the customer database. Approved recovery replays a committed result without calling business code. This does not certify external effects or platform completion.
   */
  transaction_receipt?: 'postgres_v1';
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  path: string;
  owner: 'platform_tenant';
  /**
   * Bundled JSON Schema 2020-12; only local document references are supported.
   */
  input_schema: any;
  /**
   * Bundled schema for the business result selected by the execution target.
   */
  output_schema: any;
  progress_stages: Array<string>;
  completion_webhook_id?: string;
  recovery?: 'reconcile_on_unknown' | 'safe_retry';
};

