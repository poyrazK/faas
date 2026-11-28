from typing import Literal

ObsServingProbeDiagnosticsReason = Literal[
    "coverage_unavailable", "diagnostics_inconsistent", "diagnostics_missing", "diagnostics_not_current", "query_failed"
]

OBS_SERVING_PROBE_DIAGNOSTICS_REASON_VALUES: set[ObsServingProbeDiagnosticsReason] = {
    "coverage_unavailable",
    "diagnostics_inconsistent",
    "diagnostics_missing",
    "diagnostics_not_current",
    "query_failed",
}


def check_obs_serving_probe_diagnostics_reason(value: str) -> ObsServingProbeDiagnosticsReason:
    if value in OBS_SERVING_PROBE_DIAGNOSTICS_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_SERVING_PROBE_DIAGNOSTICS_REASON_VALUES!r}")
