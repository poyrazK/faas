/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One request attribute condition. gregale.country (ISO 3166-1 alpha-2) and gregale.ua_family are verified by the gateway; app.<name> attributes are supplied by the application after it authenticates the user. A missing attribute never matches, including not_in. ends_with applies only to app.* attributes.
 */
export type FlagCondition = {
  attribute: string;
  op: 'in' | 'not_in' | 'ends_with';
  values: Array<string>;
};

