from typing import Literal

ObsServingProbeDiagnosticsFirstFailureCode = Literal[
    "body_mismatch", "connection", "dns", "http_status", "latency", "none", "timeout", "tls", "unknown"
]

OBS_SERVING_PROBE_DIAGNOSTICS_FIRST_FAILURE_CODE_VALUES: set[ObsServingProbeDiagnosticsFirstFailureCode] = {
    "body_mismatch",
    "connection",
    "dns",
    "http_status",
    "latency",
    "none",
    "timeout",
    "tls",
    "unknown",
}


def check_obs_serving_probe_diagnostics_first_failure_code(value: str) -> ObsServingProbeDiagnosticsFirstFailureCode:
    if value in OBS_SERVING_PROBE_DIAGNOSTICS_FIRST_FAILURE_CODE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_DIAGNOSTICS_FIRST_FAILURE_CODE_VALUES!r}"
    )
