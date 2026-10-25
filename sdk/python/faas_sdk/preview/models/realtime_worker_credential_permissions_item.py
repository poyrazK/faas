from typing import Literal

RealtimeWorkerCredentialPermissionsItem = Literal[
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

REALTIME_WORKER_CREDENTIAL_PERMISSIONS_ITEM_VALUES: set[RealtimeWorkerCredentialPermissionsItem] = {
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


def check_realtime_worker_credential_permissions_item(value: str) -> RealtimeWorkerCredentialPermissionsItem:
    if value in REALTIME_WORKER_CREDENTIAL_PERMISSIONS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_WORKER_CREDENTIAL_PERMISSIONS_ITEM_VALUES!r}"
    )
