/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeDiagnosticGroup } from './ObsRuntimeDiagnosticGroup.js';
/**
 * Explicit source-side selection acknowledgement. Absent on default requests to preserve compatibility. Selected and not_selected are disjoint canonical subsequences partitioning the groups supported by the role. A selected group can still be unavailable; a not_selected group is intentionally omitted. Selection never suppresses core identity, readiness, process or loop evidence, expands a budget, or changes a deployment gate.
 */
export type ObsRuntimeDiagnostics = {
  scope: 'selected_runtime_diagnostics';
  selected: Array<ObsRuntimeDiagnosticGroup>;
  not_selected: Array<ObsRuntimeDiagnosticGroup>;
};

