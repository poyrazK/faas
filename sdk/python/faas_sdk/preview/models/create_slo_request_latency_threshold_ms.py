from typing import Literal

CreateSLORequestLatencyThresholdMs = Literal[5, 10, 25, 50, 100, 250, 500, 1000, 2000, 5000, 10000]

CREATE_SLO_REQUEST_LATENCY_THRESHOLD_MS_VALUES: set[CreateSLORequestLatencyThresholdMs] = {
    5,
    10,
    25,
    50,
    100,
    250,
    500,
    1000,
    2000,
    5000,
    10000,
}


def check_create_slo_request_latency_threshold_ms(value: int) -> CreateSLORequestLatencyThresholdMs:
    if value in CREATE_SLO_REQUEST_LATENCY_THRESHOLD_MS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_SLO_REQUEST_LATENCY_THRESHOLD_MS_VALUES!r}")
