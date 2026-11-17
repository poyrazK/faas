from typing import Literal

ObsRuntimeProcessItemComparisonReason = Literal[
    "previous_evidence_unavailable", "sample_clock_regressed", "start_estimate_regressed", "unexpected_target"
]

OBS_RUNTIME_PROCESS_ITEM_COMPARISON_REASON_VALUES: set[ObsRuntimeProcessItemComparisonReason] = {
    "previous_evidence_unavailable",
    "sample_clock_regressed",
    "start_estimate_regressed",
    "unexpected_target",
}


def check_obs_runtime_process_item_comparison_reason(value: str) -> ObsRuntimeProcessItemComparisonReason:
    if value in OBS_RUNTIME_PROCESS_ITEM_COMPARISON_REASON_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PROCESS_ITEM_COMPARISON_REASON_VALUES!r}"
    )
