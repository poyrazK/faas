from typing import Literal

ObsRuntimeCommandCronTimingReason = Literal[
    "comparison_not_requested",
    "cron_timing_evidence_unavailable",
    "cron_timing_increases_observed",
    "incomplete_cron_timing_evidence",
]

OBS_RUNTIME_COMMAND_CRON_TIMING_REASON_VALUES: set[ObsRuntimeCommandCronTimingReason] = {
    "comparison_not_requested",
    "cron_timing_evidence_unavailable",
    "cron_timing_increases_observed",
    "incomplete_cron_timing_evidence",
}


def check_obs_runtime_command_cron_timing_reason(value: str) -> ObsRuntimeCommandCronTimingReason:
    if value in OBS_RUNTIME_COMMAND_CRON_TIMING_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_TIMING_REASON_VALUES!r}")
