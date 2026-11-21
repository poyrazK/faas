from typing import Literal

ObsRuntimeCommandCronOutcomeName = Literal[
    "account_held",
    "canceled",
    "coalesced",
    "deployment_unavailable",
    "disabled",
    "error",
    "exclusive_pending",
    "invalid_schedule",
    "legacy_error",
    "legacy_queued",
    "legacy_skipped",
    "legacy_stale",
    "lookup_error",
    "missed_deadline",
    "no_live_deployment",
    "not_due",
    "not_owned",
    "plan_limit",
    "queued",
    "rejected",
    "replacement_wait",
    "skipped_overlap",
    "stale",
    "store_unavailable",
    "success",
    "unknown",
]

OBS_RUNTIME_COMMAND_CRON_OUTCOME_NAME_VALUES: set[ObsRuntimeCommandCronOutcomeName] = {
    "account_held",
    "canceled",
    "coalesced",
    "deployment_unavailable",
    "disabled",
    "error",
    "exclusive_pending",
    "invalid_schedule",
    "legacy_error",
    "legacy_queued",
    "legacy_skipped",
    "legacy_stale",
    "lookup_error",
    "missed_deadline",
    "no_live_deployment",
    "not_due",
    "not_owned",
    "plan_limit",
    "queued",
    "rejected",
    "replacement_wait",
    "skipped_overlap",
    "stale",
    "store_unavailable",
    "success",
    "unknown",
}


def check_obs_runtime_command_cron_outcome_name(value: str) -> ObsRuntimeCommandCronOutcomeName:
    if value in OBS_RUNTIME_COMMAND_CRON_OUTCOME_NAME_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_OUTCOME_NAME_VALUES!r}")
