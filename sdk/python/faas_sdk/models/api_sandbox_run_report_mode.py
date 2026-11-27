from typing import Literal

APISandboxRunReportMode = Literal["automated", "partner"]

API_SANDBOX_RUN_REPORT_MODE_VALUES: set[APISandboxRunReportMode] = {
    "automated",
    "partner",
}


def check_api_sandbox_run_report_mode(value: str) -> APISandboxRunReportMode:
    if value in API_SANDBOX_RUN_REPORT_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_RUN_REPORT_MODE_VALUES!r}")
