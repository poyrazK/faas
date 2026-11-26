/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxScenario } from './APISandboxScenario.js';
/**
 * Saved library scenario with its private provenance, when available.
 */
export type APISandboxLibraryScenario = {
  id: string;
  scenario: APISandboxScenario;
  /**
   * Private Debugger provenance of this scenario.
   */
  source_request_id?: string;
};

