/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Work request admitted under a named exclusive-operation policy.
 */
export type ExclusiveOperationRequest = {
  policy: string;
  /**
   * JSON scalar used only as a business coordination key; account and tenant scope come from authenticated platform context.
   */
  key: (string | number | boolean);
  /**
   * Optional identity for joining accepted app invocations only when their invocation intents are equivalent.
   */
  equivalence_key?: string;
  invocation: {
    payload?: Record<string, any>;
    headers?: Record<string, string>;
    method?: string;
    path?: string;
  };
};

