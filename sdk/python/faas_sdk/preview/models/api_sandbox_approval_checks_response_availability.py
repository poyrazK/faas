from typing import Literal

APISandboxApprovalChecksResponseAvailability = Literal[
    "no_baseline", "no_scenario", "not_waiting", "ready", "unsupported"
]

API_SANDBOX_APPROVAL_CHECKS_RESPONSE_AVAILABILITY_VALUES: set[APISandboxApprovalChecksResponseAvailability] = {
    "no_baseline",
    "no_scenario",
    "not_waiting",
    "ready",
    "unsupported",
}


def check_api_sandbox_approval_checks_response_availability(value: str) -> APISandboxApprovalChecksResponseAvailability:
    if value in API_SANDBOX_APPROVAL_CHECKS_RESPONSE_AVAILABILITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {API_SANDBOX_APPROVAL_CHECKS_RESPONSE_AVAILABILITY_VALUES!r}"
    )
