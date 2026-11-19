/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Closed advisory group; core build identity, readiness, process and loop evidence are always collected. V1 has no selectable groups; v2 supports these names.
 */
export type ObsRuntimeDiagnosticGroup = 'builder_health' | 'handoff_health' | 'build_recovery_health' | 'app_task_health' | 'command_cron_health' | 'command_cron_outcomes' | 'http_cron_delivery' | 'delayed_task_dispatch' | 'command_cron_timing' | 'app_task_results' | 'app_task_operations' | 'post_capture_health' | 'snapshot_prime_health' | 'preparation_health';
