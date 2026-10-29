from typing import Literal

ObsInputTargetsReason = Literal[
    "expectation_unavailable",
    "invalid_evidence",
    "inventory_count_mismatch",
    "scrape_coverage_gap",
    "source_unavailable",
]

OBS_INPUT_TARGETS_REASON_VALUES: set[ObsInputTargetsReason] = {
    "expectation_unavailable",
    "invalid_evidence",
    "inventory_count_mismatch",
    "scrape_coverage_gap",
    "source_unavailable",
}


def check_obs_input_targets_reason(value: str) -> ObsInputTargetsReason:
    if value in OBS_INPUT_TARGETS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_TARGETS_REASON_VALUES!r}")
