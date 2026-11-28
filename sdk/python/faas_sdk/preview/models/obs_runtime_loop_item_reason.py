from typing import Literal

ObsRuntimeLoopItemReason = Literal[
    "conflicting_loop_series",
    "conflicting_targets",
    "expected_target_missing",
    "invalid_evidence",
    "invalid_loop_age",
    "invalid_sample_time",
    "invalid_stall_value",
    "loop_age_missing",
    "loop_sample_before_boundary",
    "loop_sample_stale",
    "loop_stall_missing",
    "scrape_before_boundary",
    "scrape_down",
    "scrape_stale",
    "source_unavailable",
]

OBS_RUNTIME_LOOP_ITEM_REASON_VALUES: set[ObsRuntimeLoopItemReason] = {
    "conflicting_loop_series",
    "conflicting_targets",
    "expected_target_missing",
    "invalid_evidence",
    "invalid_loop_age",
    "invalid_sample_time",
    "invalid_stall_value",
    "loop_age_missing",
    "loop_sample_before_boundary",
    "loop_sample_stale",
    "loop_stall_missing",
    "scrape_before_boundary",
    "scrape_down",
    "scrape_stale",
    "source_unavailable",
}


def check_obs_runtime_loop_item_reason(value: str) -> ObsRuntimeLoopItemReason:
    if value in OBS_RUNTIME_LOOP_ITEM_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_LOOP_ITEM_REASON_VALUES!r}")
