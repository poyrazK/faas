from typing import Literal

AppPrivateNetworkAttachmentStatus = Literal["error", "pending", "ready"]

APP_PRIVATE_NETWORK_ATTACHMENT_STATUS_VALUES: set[AppPrivateNetworkAttachmentStatus] = {
    "error",
    "pending",
    "ready",
}


def check_app_private_network_attachment_status(value: str) -> AppPrivateNetworkAttachmentStatus:
    if value in APP_PRIVATE_NETWORK_ATTACHMENT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_PRIVATE_NETWORK_ATTACHMENT_STATUS_VALUES!r}")
