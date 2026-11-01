/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Labels to set and keys to remove, applied together. Setting a key to its current value and removing an absent key change nothing. A key may not be both set and removed, and keys under gregale.dev/ cannot be changed.
 */
export type LabelChange = {
  /**
   * Keys to set and their values.
   */
  set?: Record<string, string>;
  /**
   * Keys to remove.
   */
  remove?: Array<string>;
};

