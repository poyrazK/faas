from typing import Literal

PreviewEnvironmentStatusResponsePhase = Literal["building", "closed", "failed", "live"]

PREVIEW_ENVIRONMENT_STATUS_RESPONSE_PHASE_VALUES: set[PreviewEnvironmentStatusResponsePhase] = {
    "building",
    "closed",
    "failed",
    "live",
}


def check_preview_environment_status_response_phase(value: str) -> PreviewEnvironmentStatusResponsePhase:
    if value in PREVIEW_ENVIRONMENT_STATUS_RESPONSE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREVIEW_ENVIRONMENT_STATUS_RESPONSE_PHASE_VALUES!r}")
