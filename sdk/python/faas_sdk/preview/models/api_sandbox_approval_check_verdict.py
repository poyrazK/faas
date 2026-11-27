from typing import Literal

APISandboxApprovalCheckVerdict = Literal["inconclusive", "passed", "regression", "running", "stale"]

API_SANDBOX_APPROVAL_CHECK_VERDICT_VALUES: set[APISandboxApprovalCheckVerdict] = {
    "inconclusive",
    "passed",
    "regression",
    "running",
    "stale",
}


def check_api_sandbox_approval_check_verdict(value: str) -> APISandboxApprovalCheckVerdict:
    if value in API_SANDBOX_APPROVAL_CHECK_VERDICT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_APPROVAL_CHECK_VERDICT_VALUES!r}")
