/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Named business webhook delivery proposed by a managed HTTP operation handler.
 */
export type ManagedOperationEffect = {
  /**
   * Unique within this operation completion.
   */
  name: string;
  /**
   * Explicit operation.effect subscriber owned by the authenticated operation scope.
   */
  webhook_id: string;
  /**
   * Business event type carried inside the operation.effect payload.
   */
  type: string;
  /**
   * JSON business data bounded to 64 KiB.
   */
  payload: any;
};

