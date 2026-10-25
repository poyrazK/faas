from typing import Literal

TraceSpanStatus = Literal["error", "ok"]

TRACE_SPAN_STATUS_VALUES: set[TraceSpanStatus] = {
    "error",
    "ok",
}


def check_trace_span_status(value: str) -> TraceSpanStatus:
    if value in TRACE_SPAN_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRACE_SPAN_STATUS_VALUES!r}")
