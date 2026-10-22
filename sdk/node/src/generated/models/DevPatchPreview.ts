/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Set only on the response to a developer source upload (`gregale dev`). Reports whether the sync can be applied as a live source patch to the deployment that was live at upload time (ADR-740) and, when it was published as one, whether its build waits for the developer to stop saving (ADR-20261021221906).
 */
export type DevPatchPreview = {
  eligible: boolean;
  /**
   * Why the sync could not use a live patch. Absent when eligible.
   */
  reason?: 'not_railpack' | 'plan_unreadable' | 'no_source_layer' | 'build_command' | 'source_not_deployed' | 'no_live_build' | 'no_base_manifest' | 'full_snapshot' | 'rebuild_input_changed' | 'unsupported_entry' | 'patch_too_large' | 'unsupported_source_map';
  /**
   * Files added, modified or deleted inside the build's source root.
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
  /**
   * Set when this sync's build waits behind its live patch (ADR-20261021221906). The build starts at this time unless a later sync replaces it first; a patch that fails to apply starts it at once.
   */
  build_deferred_until?: string;
};

