from typing import Literal

APISandboxComparisonReportFailureSide = Literal["baseline", "candidate"]

API_SANDBOX_COMPARISON_REPORT_FAILURE_SIDE_VALUES: set[APISandboxComparisonReportFailureSide] = {
    "baseline",
    "candidate",
}


def check_api_sandbox_comparison_report_failure_side(value: str) -> APISandboxComparisonReportFailureSide:
    if value in API_SANDBOX_COMPARISON_REPORT_FAILURE_SIDE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {API_SANDBOX_COMPARISON_REPORT_FAILURE_SIDE_VALUES!r}"
    )
