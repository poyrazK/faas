from typing import Literal

ProfileRequestMixSnapshotStatus = Literal["captured", "partial", "unavailable"]

PROFILE_REQUEST_MIX_SNAPSHOT_STATUS_VALUES: set[ProfileRequestMixSnapshotStatus] = {
    "captured",
    "partial",
    "unavailable",
}


def check_profile_request_mix_snapshot_status(value: str) -> ProfileRequestMixSnapshotStatus:
    if value in PROFILE_REQUEST_MIX_SNAPSHOT_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PROFILE_REQUEST_MIX_SNAPSHOT_STATUS_VALUES!r}")
