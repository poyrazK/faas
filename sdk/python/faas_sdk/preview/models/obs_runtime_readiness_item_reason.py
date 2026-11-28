from typing import Literal

ObsRuntimeReadinessItemReason = Literal[
    "conflicting_readiness",
    "conflicting_targets",
    "credentials",
    "database",
    "draining",
    "expected_target_missing",
    "grpc",
    "invalid_evidence",
    "invalid_sample_time",
    "other",
    "process",
    "readiness_before_boundary",
    "readiness_missing",
    "readiness_stale",
    "ready",
    "scrape_before_boundary",
    "scrape_down",
    "scrape_stale",
    "source_unavailable",
    "stale",
    "storage",
    "vmmd",
]

OBS_RUNTIME_READINESS_ITEM_REASON_VALUES: set[ObsRuntimeReadinessItemReason] = {
    "conflicting_readiness",
    "conflicting_targets",
    "credentials",
    "database",
    "draining",
    "expected_target_missing",
    "grpc",
    "invalid_evidence",
    "invalid_sample_time",
    "other",
    "process",
    "readiness_before_boundary",
    "readiness_missing",
    "readiness_stale",
    "ready",
    "scrape_before_boundary",
    "scrape_down",
    "scrape_stale",
    "source_unavailable",
    "stale",
    "storage",
    "vmmd",
}


def check_obs_runtime_readiness_item_reason(value: str) -> ObsRuntimeReadinessItemReason:
    if value in OBS_RUNTIME_READINESS_ITEM_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_READINESS_ITEM_REASON_VALUES!r}")
