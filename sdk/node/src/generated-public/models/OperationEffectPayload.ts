/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data of the operation.effect webhook. Identity comes from the platform-owned operation.
 */
export type OperationEffectPayload = {
  operation_id: string;
  app_id: string;
  platform_tenant_id?: string;
  generation: number;
  name: string;
  type: string;
  /**
   * Handler-supplied business event data.
   */
  data: any;
};

