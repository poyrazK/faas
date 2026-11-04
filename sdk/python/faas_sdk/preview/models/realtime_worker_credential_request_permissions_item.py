from typing import Literal

RealtimeWorkerCredentialRequestPermissionsItem = Literal[
    "claim",
    "complete",
    "dead_letters",
    "delete",
    "drain",
    "heartbeat",
    "pause",
    "release",
    "renew",
    "replay",
    "resume",
    "resync",
    "retry",
    "status",
]

REALTIME_WORKER_CREDENTIAL_REQUEST_PERMISSIONS_ITEM_VALUES: set[RealtimeWorkerCredentialRequestPermissionsItem] = {
    "claim",
    "complete",
    "dead_letters",
    "delete",
    "drain",
    "heartbeat",
    "pause",
    "release",
    "renew",
    "replay",
    "resume",
    "resync",
    "retry",
    "status",
}


def check_realtime_worker_credential_request_permissions_item(
    value: str,
) -> RealtimeWorkerCredentialRequestPermissionsItem:
    if value in REALTIME_WORKER_CREDENTIAL_REQUEST_PERMISSIONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_WORKER_CREDENTIAL_REQUEST_PERMISSIONS_ITEM_VALUES!r}"
    )
