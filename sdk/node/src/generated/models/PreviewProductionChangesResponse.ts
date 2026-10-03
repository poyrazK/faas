/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PreviewArtifactResponse } from './PreviewArtifactResponse.js';
/**
 * Non-secret preview differences from the production parent.
 */
export type PreviewProductionChangesResponse = {
  artifact_changed: boolean;
  preview_artifact: PreviewArtifactResponse;
  production_artifact: PreviewArtifactResponse;
  configuration_changed_groups: Array<'runtime' | 'resources' | 'scaling' | 'routing' | 'policies'>;
};

