from typing import Literal

DeploymentCronActivationStatusReason = Literal[
    "activation_discarded",
    "awaiting_activation",
    "awaiting_full_traffic",
    "awaiting_readiness",
    "awaiting_rollout",
    "competing_traffic",
    "cron_changed",
    "cron_guard_conflict",
    "deployment_canceled",
    "deployment_failed",
    "deployment_superseded",
    "identity_conflict",
    "intent_superseded",
    "non_production",
    "plan_unavailable",
    "quota_changed",
    "rollout_aborted",
]

DEPLOYMENT_CRON_ACTIVATION_STATUS_REASON_VALUES: set[DeploymentCronActivationStatusReason] = {
    "activation_discarded",
    "awaiting_activation",
    "awaiting_full_traffic",
    "awaiting_readiness",
    "awaiting_rollout",
    "competing_traffic",
    "cron_changed",
    "cron_guard_conflict",
    "deployment_canceled",
    "deployment_failed",
    "deployment_superseded",
    "identity_conflict",
    "intent_superseded",
    "non_production",
    "plan_unavailable",
    "quota_changed",
    "rollout_aborted",
}


def check_deployment_cron_activation_status_reason(value: str) -> DeploymentCronActivationStatusReason:
    if value in DEPLOYMENT_CRON_ACTIVATION_STATUS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_CRON_ACTIVATION_STATUS_REASON_VALUES!r}")
