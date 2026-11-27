from typing import Literal

APISandboxReceiverInviteStatus = Literal["expired", "pending", "ready", "verified"]

API_SANDBOX_RECEIVER_INVITE_STATUS_VALUES: set[APISandboxReceiverInviteStatus] = {
    "expired",
    "pending",
    "ready",
    "verified",
}


def check_api_sandbox_receiver_invite_status(value: str) -> APISandboxReceiverInviteStatus:
    if value in API_SANDBOX_RECEIVER_INVITE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_RECEIVER_INVITE_STATUS_VALUES!r}")
