from typing import Literal

APISandboxSuiteRunReportFailureCode = Literal["approval_inputs_changed", "entitlement_changed", "timed_out"]

API_SANDBOX_SUITE_RUN_REPORT_FAILURE_CODE_VALUES: set[APISandboxSuiteRunReportFailureCode] = {
    "approval_inputs_changed",
    "entitlement_changed",
    "timed_out",
}


def check_api_sandbox_suite_run_report_failure_code(value: str) -> APISandboxSuiteRunReportFailureCode:
    if value in API_SANDBOX_SUITE_RUN_REPORT_FAILURE_CODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_SUITE_RUN_REPORT_FAILURE_CODE_VALUES!r}")
