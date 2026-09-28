/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Named policy and typed application key for one async invocation.
 */
export type InvokeWork = {
  policy: string;
  /**
   * A bounded JSON string, number, or boolean. Equal typed values share one work lane.
   */
  key: any;
};

