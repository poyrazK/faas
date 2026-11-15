/** Portable approval/input views. Safe to import as types in browsers. */
import type { PipelineScope } from './pipeline-state.js';

export type PipelineGateKind = 'approval' | 'input';
export type PipelineGateChoice = 'approved' | 'rejected' | 'submitted' | 'expired' | 'cancelled';
export type PipelineGateOutcome<D> =
  | { readonly state: 'approved' | 'rejected' | 'submitted'; readonly actor_id: string; readonly payload: D; readonly decided_at: string }
  | { readonly state: 'expired' | 'cancelled'; readonly actor_id: string | null; readonly payload: null; readonly decided_at: string };
export interface PipelineGateIntent<R = unknown> {
  readonly version: 1; readonly id: string; readonly kind: PipelineGateKind;
  readonly scope: PipelineScope; readonly workflow_run_id: string; readonly owner_id: string;
  readonly pipeline_sha256: string; readonly input_sha256: string; readonly name: string;
  readonly contract_sha256: string; readonly review: R; readonly review_sha256: string;
  readonly required_roles: readonly string[]; readonly timeout_ms: number; readonly binding_sha256: string;
}
export interface PipelineGateRecord<R = unknown, D = unknown> {
  readonly intent: PipelineGateIntent<R>; readonly expires_at: string;
  readonly decision: PipelineGateOutcome<D> | null; readonly delivery: 'pending' | 'delivered' | 'closed';
}
export interface PipelineGateActor {
  readonly actor_id: string; readonly owner_id: string; readonly roles: readonly string[];
}
export interface PipelineGateView<R, D> {
  readonly can_decide: boolean;
  readonly id: string; readonly run_id: string; readonly name: string; readonly kind: PipelineGateKind;
  readonly review: R; readonly binding_sha256: string; readonly expires_at: string;
  readonly decision: PipelineGateOutcome<D> | null; readonly delivery: PipelineGateRecord['delivery'];
}
