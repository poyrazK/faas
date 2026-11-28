from typing import Literal

ObsRuntimeProcessReason = Literal[
    "awaiting_comparison", "incomplete_continuity_evidence", "observed_process_change", "process_evidence_unavailable"
]

OBS_RUNTIME_PROCESS_REASON_VALUES: set[ObsRuntimeProcessReason] = {
    "awaiting_comparison",
    "incomplete_continuity_evidence",
    "observed_process_change",
    "process_evidence_unavailable",
}


def check_obs_runtime_process_reason(value: str) -> ObsRuntimeProcessReason:
    if value in OBS_RUNTIME_PROCESS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PROCESS_REASON_VALUES!r}")
