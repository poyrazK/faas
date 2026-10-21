/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Narrows a server key. Absent fields keep full access; live reads use relations and subjects.
 */
export type DataAPIServerKeyPermissionSet = {
  access?: 'read' | 'write';
  relations?: Array<string>;
  rpcs?: Array<string>;
  subjects?: boolean;
};

