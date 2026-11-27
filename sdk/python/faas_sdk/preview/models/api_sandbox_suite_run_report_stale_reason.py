from typing import Literal

APISandboxSuiteRunReportStaleReason = Literal[
    "candidate_artifact_changed", "live_baseline_changed", "suite_changed", "suite_scenario_changed"
]

API_SANDBOX_SUITE_RUN_REPORT_STALE_REASON_VALUES: set[APISandboxSuiteRunReportStaleReason] = {
    "candidate_artifact_changed",
    "live_baseline_changed",
    "suite_changed",
    "suite_scenario_changed",
}


def check_api_sandbox_suite_run_report_stale_reason(value: str) -> APISandboxSuiteRunReportStaleReason:
    if value in API_SANDBOX_SUITE_RUN_REPORT_STALE_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_SUITE_RUN_REPORT_STALE_REASON_VALUES!r}")
