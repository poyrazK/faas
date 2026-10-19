/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Declares a workflow callback step as an approval decided by the account or the operation's customer.
 */
export type OperationApprovalSpec = {
  step: string;
  approver: 'account' | 'customer';
};

