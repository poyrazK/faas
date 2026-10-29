from typing import Literal

SidecarDiskIoProfile = Literal["high", "low", "standard"]

SIDECAR_DISK_IO_PROFILE_VALUES: set[SidecarDiskIoProfile] = {
    "high",
    "low",
    "standard",
}


def check_sidecar_disk_io_profile(value: str) -> SidecarDiskIoProfile:
    if value in SIDECAR_DISK_IO_PROFILE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SIDECAR_DISK_IO_PROFILE_VALUES!r}")
