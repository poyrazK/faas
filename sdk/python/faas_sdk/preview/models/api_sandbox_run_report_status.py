from typing import Literal

APISandboxRunReportStatus = Literal[
    "awaiting_partner",
    "awaiting_partner_outcome",
    "checking_state",
    "failed",
    "interrupted",
    "passed",
    "queued",
    "running",
]

API_SANDBOX_RUN_REPORT_STATUS_VALUES: set[APISandboxRunReportStatus] = {
    "awaiting_partner",
    "awaiting_partner_outcome",
    "checking_state",
    "failed",
    "interrupted",
    "passed",
    "queued",
    "running",
}


def check_api_sandbox_run_report_status(value: str) -> APISandboxRunReportStatus:
    if value in API_SANDBOX_RUN_REPORT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_RUN_REPORT_STATUS_VALUES!r}")
