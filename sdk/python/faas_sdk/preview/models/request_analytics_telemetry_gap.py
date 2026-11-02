from typing import Literal

RequestAnalyticsTelemetryGap = Literal[
    "telemetry_coverage_missing",
    "telemetry_coverage_stale",
    "telemetry_disabled",
    "telemetry_ingestion_pending",
    "telemetry_sampled",
    "telemetry_window_incomplete",
]

REQUEST_ANALYTICS_TELEMETRY_GAP_VALUES: set[RequestAnalyticsTelemetryGap] = {
    "telemetry_coverage_missing",
    "telemetry_coverage_stale",
    "telemetry_disabled",
    "telemetry_ingestion_pending",
    "telemetry_sampled",
    "telemetry_window_incomplete",
}


def check_request_analytics_telemetry_gap(value: str) -> RequestAnalyticsTelemetryGap:
    if value in REQUEST_ANALYTICS_TELEMETRY_GAP_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REQUEST_ANALYTICS_TELEMETRY_GAP_VALUES!r}")
