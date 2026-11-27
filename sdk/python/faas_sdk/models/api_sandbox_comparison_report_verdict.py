from typing import Literal

APISandboxComparisonReportVerdict = Literal["inconclusive", "passed", "regression", "running"]

API_SANDBOX_COMPARISON_REPORT_VERDICT_VALUES: set[APISandboxComparisonReportVerdict] = {
    "inconclusive",
    "passed",
    "regression",
    "running",
}


def check_api_sandbox_comparison_report_verdict(value: str) -> APISandboxComparisonReportVerdict:
    if value in API_SANDBOX_COMPARISON_REPORT_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_COMPARISON_REPORT_VERDICT_VALUES!r}")
