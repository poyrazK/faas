/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One recorded row change. The id is the change's UUID in the database; the platform derives the producer key data-api.change.ID from it.
 */
export type DataAPIChangeEvent = {
  id: string;
  /**
   * SCHEMA.TABLE.inserted
   */
  type: string;
  /**
   * When the change was recorded.
   */
  time?: string;
  /**
   * The change
   */
  data: any;
};

