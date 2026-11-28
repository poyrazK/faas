from typing import Literal

ObsRuntimeProcessItemReason = Literal[
    "conflicting_targets",
    "conflicting_uptime",
    "expected_target_missing",
    "invalid_evidence",
    "invalid_sample_time",
    "invalid_uptime",
    "scrape_before_boundary",
    "scrape_down",
    "scrape_stale",
    "source_unavailable",
    "uptime_before_boundary",
    "uptime_missing",
    "uptime_stale",
]

OBS_RUNTIME_PROCESS_ITEM_REASON_VALUES: set[ObsRuntimeProcessItemReason] = {
    "conflicting_targets",
    "conflicting_uptime",
    "expected_target_missing",
    "invalid_evidence",
    "invalid_sample_time",
    "invalid_uptime",
    "scrape_before_boundary",
    "scrape_down",
    "scrape_stale",
    "source_unavailable",
    "uptime_before_boundary",
    "uptime_missing",
    "uptime_stale",
}


def check_obs_runtime_process_item_reason(value: str) -> ObsRuntimeProcessItemReason:
    if value in OBS_RUNTIME_PROCESS_ITEM_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PROCESS_ITEM_REASON_VALUES!r}")
