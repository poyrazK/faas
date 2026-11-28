from typing import Literal

ObsRuntimeLoopHealthReason = Literal["incomplete_loop_evidence", "loop_evidence_unavailable", "reported_loop_stall"]

OBS_RUNTIME_LOOP_HEALTH_REASON_VALUES: set[ObsRuntimeLoopHealthReason] = {
    "incomplete_loop_evidence",
    "loop_evidence_unavailable",
    "reported_loop_stall",
}


def check_obs_runtime_loop_health_reason(value: str) -> ObsRuntimeLoopHealthReason:
    if value in OBS_RUNTIME_LOOP_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_LOOP_HEALTH_REASON_VALUES!r}")
