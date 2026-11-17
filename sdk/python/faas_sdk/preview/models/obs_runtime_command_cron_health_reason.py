from typing import Literal

ObsRuntimeCommandCronHealthReason = Literal[
    "command_cron_evidence_observed", "command_cron_evidence_unavailable", "incomplete_command_cron_evidence"
]

OBS_RUNTIME_COMMAND_CRON_HEALTH_REASON_VALUES: set[ObsRuntimeCommandCronHealthReason] = {
    "command_cron_evidence_observed",
    "command_cron_evidence_unavailable",
    "incomplete_command_cron_evidence",
}


def check_obs_runtime_command_cron_health_reason(value: str) -> ObsRuntimeCommandCronHealthReason:
    if value in OBS_RUNTIME_COMMAND_CRON_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_HEALTH_REASON_VALUES!r}")
