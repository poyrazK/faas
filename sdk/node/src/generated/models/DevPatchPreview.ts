/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Set only on the response to a developer source upload (`gregale dev`). Reports whether the sync could have been applied as a live source patch to the deployment that was live at upload time (ADR-740 phase 1). It is a measurement only; the normal developer build always runs.
 */
export type DevPatchPreview = {
  eligible: boolean;
  /**
   * Why the sync could not use a live patch. Absent when eligible.
   */
  reason?: 'not_railpack' | 'plan_unreadable' | 'no_source_layer' | 'build_command' | 'source_not_deployed' | 'no_live_build' | 'no_base_manifest' | 'full_snapshot' | 'rebuild_input_changed' | 'unsupported_entry' | 'patch_too_large' | 'unsupported_source_map';
  /**
   * Files added
   */
  changed_paths: number;
  /**
   * Total size of the added or modified files.
   */
  patch_bytes: number;
  /**
   * Set when this sync published a live patch; poll getDevPatchStatus with it.
   */
  generation?: number;
};

