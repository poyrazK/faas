from typing import Literal

DebugRequestCorrelationStagePhase = Literal["billing", "downstream", "edge", "guest", "queue", "wake"]

DEBUG_REQUEST_CORRELATION_STAGE_PHASE_VALUES: set[DebugRequestCorrelationStagePhase] = {
    "billing",
    "downstream",
    "edge",
    "guest",
    "queue",
    "wake",
}


def check_debug_request_correlation_stage_phase(value: str) -> DebugRequestCorrelationStagePhase:
    if value in DEBUG_REQUEST_CORRELATION_STAGE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_REQUEST_CORRELATION_STAGE_PHASE_VALUES!r}")
