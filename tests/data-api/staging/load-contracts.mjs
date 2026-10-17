import { check } from '../load/contracts.mjs'

// Canonical bounds are in pkg/api/limits.go; load.test.mjs checks these mirrors.
export const hostedBounds = Object.freeze({ runMs: 1800000, scenarioMs: 60000, parkMs: 900000, restartMs: 300000, pauseMs: 3000, responseBytes: 1048576 })
export const hostedPhases = Object.freeze(['mixed_load', 'admission_pressure', 'rate_pressure', 'lost_reply', 'managed_restart', 'park_wake', 'recovery_load'])
export const hostedCoverage = Object.freeze({
  supported: hostedPhases,
  unsupported: { deadline: 'requires_independent_owner_sql_control', disconnect: 'requires_observed_inflight_sql',
    response_limits: 'oversized_fixture_rpc_not_installed', engine_restart: 'private_engine_kill_not_available',
    sql_connection_peaks: 'serving_role_sql_observer_not_available', sql_execution_and_pool_wait: 'not_instrumented' },
})
export function hostedMetadata(value) {
  const output = {}
  for (const key of ['source_sha256', 'sdk_sha256', 'harness_sha256', 'fingerprint']) {
    check(typeof value?.[key] === 'string' && /^[a-f0-9]{64}$/.test(value[key]), 'source_identity_invalid'); output[key] = value[key]
  }
  check(typeof value.source_commit === 'string' && /^[a-f0-9]{40}$/.test(value.source_commit), 'source_identity_invalid')
  for (const key of ['deployment_id', 'build_id', 'builder_node_id']) {
    check(typeof value[key] === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(value[key]), 'source_identity_invalid'); output[key] = value[key]
  }
  check(typeof value.node_version === 'string' && /^v\d+\.\d+\.\d+$/.test(value.node_version), 'source_identity_invalid')
  return { ...output, source_commit: value.source_commit, node_version: value.node_version,
    rate_per_second: 2, rate_burst: 8, restart_model: 'fresh_managed_restart', native_host_qualification: 'external_prerequisite' }
}
export const hostedProfile = Object.freeze({ scope: 'hosted-data-api-load', qualification: 'isolated_staging_only',
  runMs: hostedBounds.runMs, phaseNames: hostedPhases, coverage: hostedCoverage, metadata: hostedMetadata,
  scenarioMs: name => name === 'park_wake' ? hostedBounds.parkMs + hostedBounds.scenarioMs : name === 'managed_restart' ? hostedBounds.restartMs + hostedBounds.scenarioMs : hostedBounds.scenarioMs })
