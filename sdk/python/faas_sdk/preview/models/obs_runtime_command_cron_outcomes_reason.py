from typing import Literal

ObsRuntimeCommandCronOutcomesReason = Literal[
    "comparison_not_requested",
    "cron_outcome_evidence_unavailable",
    "cron_outcome_increases_observed",
    "incomplete_cron_outcome_evidence",
]

OBS_RUNTIME_COMMAND_CRON_OUTCOMES_REASON_VALUES: set[ObsRuntimeCommandCronOutcomesReason] = {
    "comparison_not_requested",
    "cron_outcome_evidence_unavailable",
    "cron_outcome_increases_observed",
    "incomplete_cron_outcome_evidence",
}


def check_obs_runtime_command_cron_outcomes_reason(value: str) -> ObsRuntimeCommandCronOutcomesReason:
    if value in OBS_RUNTIME_COMMAND_CRON_OUTCOMES_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_COMMAND_CRON_OUTCOMES_REASON_VALUES!r}")
