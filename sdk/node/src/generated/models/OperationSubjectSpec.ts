/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional public business reference extracted once from validated input on new admission. This metadata does not authorize access to the business entity.
 */
export type OperationSubjectSpec = {
  type: string;
  /**
   * JSON Pointer into input. The selected value must be a nonempty string; array indices use canonical decimal notation. Maximum 2048 UTF-8 bytes; ASCII controls are rejected.
   */
  id_from: string;
};

