/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One backward-compatible source release command authorized against destination runtime inputs. Contains a command fingerprint, never command bytes or credentials.
 */
export type ProjectPromotionMigrationPolicy = {
  app_id: string;
  slug: string;
  command_hash: string;
  compatibility: 'backward-compatible';
};

