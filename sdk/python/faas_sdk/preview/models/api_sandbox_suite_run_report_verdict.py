from typing import Literal

APISandboxSuiteRunReportVerdict = Literal["inconclusive", "passed", "regression", "running", "stale"]

API_SANDBOX_SUITE_RUN_REPORT_VERDICT_VALUES: set[APISandboxSuiteRunReportVerdict] = {
    "inconclusive",
    "passed",
    "regression",
    "running",
    "stale",
}


def check_api_sandbox_suite_run_report_verdict(value: str) -> APISandboxSuiteRunReportVerdict:
    if value in API_SANDBOX_SUITE_RUN_REPORT_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_SUITE_RUN_REPORT_VERDICT_VALUES!r}")
