from typing import Literal

APISandboxSuiteRunsResponseAutomaticStatus = Literal[
    "approval_ended",
    "disabled",
    "queued",
    "started",
    "unavailable",
    "waiting_for_baseline",
    "waiting_for_capacity",
    "waiting_for_scenarios",
]

API_SANDBOX_SUITE_RUNS_RESPONSE_AUTOMATIC_STATUS_VALUES: set[APISandboxSuiteRunsResponseAutomaticStatus] = {
    "approval_ended",
    "disabled",
    "queued",
    "started",
    "unavailable",
    "waiting_for_baseline",
    "waiting_for_capacity",
    "waiting_for_scenarios",
}


def check_api_sandbox_suite_runs_response_automatic_status(value: str) -> APISandboxSuiteRunsResponseAutomaticStatus:
    if value in API_SANDBOX_SUITE_RUNS_RESPONSE_AUTOMATIC_STATUS_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {API_SANDBOX_SUITE_RUNS_RESPONSE_AUTOMATIC_STATUS_VALUES!r}"
    )
