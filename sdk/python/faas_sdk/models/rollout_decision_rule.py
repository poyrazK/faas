from typing import Literal

RolloutDecisionRule = Literal[
    "abort_5xx",
    "abort_cold_boot_p95",
    "abort_cpu_per_request",
    "abort_crash_loop",
    "abort_dependency_errors",
    "abort_manual",
    "abort_oom",
    "abort_p95_latency",
    "abort_post_release_5xx",
    "abort_post_release_p95_latency",
    "abort_worker_lease_expired",
    "advance_healthy",
    "advance_low_traffic",
    "advance_manual",
    "advance_no_predecessor",
    "advance_probe_evidence",
    "hold_health_alert",
    "hold_insufficient_samples",
    "hold_observation_unavailable",
    "hold_signal_unavailable",
]

ROLLOUT_DECISION_RULE_VALUES: set[RolloutDecisionRule] = {
    "abort_5xx",
    "abort_cold_boot_p95",
    "abort_cpu_per_request",
    "abort_crash_loop",
    "abort_dependency_errors",
    "abort_manual",
    "abort_oom",
    "abort_p95_latency",
    "abort_post_release_5xx",
    "abort_post_release_p95_latency",
    "abort_worker_lease_expired",
    "advance_healthy",
    "advance_low_traffic",
    "advance_manual",
    "advance_no_predecessor",
    "advance_probe_evidence",
    "hold_health_alert",
    "hold_insufficient_samples",
    "hold_observation_unavailable",
    "hold_signal_unavailable",
}


def check_rollout_decision_rule(value: str) -> RolloutDecisionRule:
    if value in ROLLOUT_DECISION_RULE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROLLOUT_DECISION_RULE_VALUES!r}")
