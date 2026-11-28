from typing import Literal

DebugRequestCorrelationStageStatus = Literal["missing", "not_applicable", "observed", "partial"]

DEBUG_REQUEST_CORRELATION_STAGE_STATUS_VALUES: set[DebugRequestCorrelationStageStatus] = {
    "missing",
    "not_applicable",
    "observed",
    "partial",
}


def check_debug_request_correlation_stage_status(value: str) -> DebugRequestCorrelationStageStatus:
    if value in DEBUG_REQUEST_CORRELATION_STAGE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEBUG_REQUEST_CORRELATION_STAGE_STATUS_VALUES!r}")
