/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCaptureProfile } from './ProfileCaptureProfile.js';
/**
 * An on-demand capture. processes counts instrumented processes that saw the capture; zero means the instance has no collector.
 */
export type ProfileCapture = {
  id: string;
  app_id: string;
  status: 'queued' | 'capturing' | 'ready' | 'failed';
  kinds: Array<'cpu' | 'heap' | 'stacks' | 'block' | 'mutex'>;
  duration_seconds: number;
  requested_instance_id?: string;
  /**
   * Who started the capture; memory_pressure captures are queued automatically when an instance runs low on memory, slow_request captures (stacks and CPU) when a request stays in flight for 10 seconds, cpu_saturation captures (CPU) when an instance uses 90% of its CPU limit for 30 seconds.
   */
  trigger?: 'manual' | 'memory_pressure' | 'slow_request' | 'cpu_saturation';
  /**
   * The x-faas-request-id of the in-flight request that started a slow_request capture.
   */
  request_id?: string;
  /**
   * The trace id that request's telemetry is stored under.
   */
  request_trace_id?: string;
  trigger_detail?: string;
  instance_id?: string;
  deployment_id?: string;
  processes: number;
  /**
   * Profiles discarded by the reply bounds.
   */
  dropped?: number;
  reason?: string;
  profiles: Array<ProfileCaptureProfile>;
  /**
   * The capture ended early, normally because the workload was OOM-killed; heap profiles are the last checkpoints before the kill.
   */
  partial?: boolean;
  /**
   * Set when the captured instance was OOM-killed within 15 minutes of the capture.
   */
  oom_killed_at?: string;
  /**
   * Memory in use when the instance was OOM-killed.
   */
  oom_peak_mb?: number;
  /**
   * The instance's plan memory limit.
   */
  oom_plan_mb?: number;
  created_at: string;
  completed_at?: string;
  expires_at: string;
};

