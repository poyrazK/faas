/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Rename payload: new slug. Old slug returns 404 immediately on the next request.
 */
export type RenameAppRequest = {
  /**
   * App slugs beginning with tag- are reserved for deployment-alias hostnames.
   */
  new_slug: string;
};

