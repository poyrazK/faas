from typing import Literal

ObsRuntimeHandoffHealthReason = Literal[
    "handoff_evidence_unavailable", "incomplete_handoff_evidence", "pending_handoffs_observed"
]

OBS_RUNTIME_HANDOFF_HEALTH_REASON_VALUES: set[ObsRuntimeHandoffHealthReason] = {
    "handoff_evidence_unavailable",
    "incomplete_handoff_evidence",
    "pending_handoffs_observed",
}


def check_obs_runtime_handoff_health_reason(value: str) -> ObsRuntimeHandoffHealthReason:
    if value in OBS_RUNTIME_HANDOFF_HEALTH_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_HANDOFF_HEALTH_REASON_VALUES!r}")
