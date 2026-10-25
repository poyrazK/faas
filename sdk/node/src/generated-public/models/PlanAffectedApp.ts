/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the ADR-124 blast-radius partition. action is closed-
 * vocabulary:
 * create — scan workload, no existing app row matches (root_dir, name).
 * update — scan workload, existing app matches.
 * remove — existing app, no scan workload, not protected by --exclude.
 * noop   — operator excluded via --exclude, or no scan change.
 * id is empty for action == create. existing_root_dir is populated
 * only when the existing app's root_dir differs from the scan root_dir
 * (monorepo collision surface).
 *
 */
export type PlanAffectedApp = {
  slug: string;
  id?: string;
  action: 'create' | 'update' | 'remove' | 'noop';
  /**
   * RootDir of the existing app row. Empty for create. Populated only when it differs from the scan-time RootDir.
   */
  existing_root_dir?: string;
};

