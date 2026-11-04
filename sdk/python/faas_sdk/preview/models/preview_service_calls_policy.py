from typing import Literal

PreviewServiceCallsPolicy = Literal["allow", "deny"]

PREVIEW_SERVICE_CALLS_POLICY_VALUES: set[PreviewServiceCallsPolicy] = {
    "allow",
    "deny",
}


def check_preview_service_calls_policy(value: str) -> PreviewServiceCallsPolicy:
    if value in PREVIEW_SERVICE_CALLS_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PREVIEW_SERVICE_CALLS_POLICY_VALUES!r}")
