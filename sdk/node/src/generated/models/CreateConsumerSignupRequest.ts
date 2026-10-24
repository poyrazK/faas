/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A customer's sign-up.
 */
export type CreateConsumerSignupRequest = {
  email: string;
  name: string;
  /**
   * One of the offered plans; may be omitted when one plan is offered.
   */
  plan_id?: string;
};

