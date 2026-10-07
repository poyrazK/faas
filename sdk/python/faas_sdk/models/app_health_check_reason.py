from typing import Literal

AppHealthCheckReason = Literal[
    "capacity_below_target",
    "collection_gap",
    "request_coverage_incomplete",
    "request_error_rate_elevated",
    "request_error_rate_severe",
    "request_errors_below_threshold",
    "request_evidence_invalid",
    "request_evidence_stale",
    "request_evidence_unavailable",
    "request_plan_restricted",
    "request_scope_unavailable",
    "request_volume_insufficient",
    "requests_observed",
    "requests_unexercised",
]

APP_HEALTH_CHECK_REASON_VALUES: set[AppHealthCheckReason] = {
    "capacity_below_target",
    "collection_gap",
    "request_coverage_incomplete",
    "request_error_rate_elevated",
    "request_error_rate_severe",
    "request_errors_below_threshold",
    "request_evidence_invalid",
    "request_evidence_stale",
    "request_evidence_unavailable",
    "request_plan_restricted",
    "request_scope_unavailable",
    "request_volume_insufficient",
    "requests_observed",
    "requests_unexercised",
}


def check_app_health_check_reason(value: str) -> AppHealthCheckReason:
    if value in APP_HEALTH_CHECK_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_CHECK_REASON_VALUES!r}")
