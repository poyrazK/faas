/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Named policy and typed application key for one durable invocation.
 */
export type InvokeWork = {
  policy: string;
  /**
   * A bounded JSON string, number, or boolean. Equal typed values share one work lane.
   */
  key: any;
  /**
   * Optional scalar shared by multiple work keys, such as a tenant ID. Defaults to key when the policy has a fairness cap.
   */
  fairness_key?: any;
};

