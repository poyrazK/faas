from typing import Literal

APISandboxReleaseGateStatus = Literal["admitted", "failed", "no_suite", "passed", "stale", "unavailable", "waiting"]

API_SANDBOX_RELEASE_GATE_STATUS_VALUES: set[APISandboxReleaseGateStatus] = {
    "admitted",
    "failed",
    "no_suite",
    "passed",
    "stale",
    "unavailable",
    "waiting",
}


def check_api_sandbox_release_gate_status(value: str) -> APISandboxReleaseGateStatus:
    if value in API_SANDBOX_RELEASE_GATE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_RELEASE_GATE_STATUS_VALUES!r}")
