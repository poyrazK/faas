from typing import Literal

APISandboxComparisonReportReason = Literal[
    "baseline_check_failed", "baseline_unavailable", "candidate_check_failed", "candidate_unavailable"
]

API_SANDBOX_COMPARISON_REPORT_REASON_VALUES: set[APISandboxComparisonReportReason] = {
    "baseline_check_failed",
    "baseline_unavailable",
    "candidate_check_failed",
    "candidate_unavailable",
}


def check_api_sandbox_comparison_report_reason(value: str) -> APISandboxComparisonReportReason:
    if value in API_SANDBOX_COMPARISON_REPORT_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_COMPARISON_REPORT_REASON_VALUES!r}")
