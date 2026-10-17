from typing import Literal

CompanionDeclarationDiskIoProfile = Literal["high", "low", "standard"]

COMPANION_DECLARATION_DISK_IO_PROFILE_VALUES: set[CompanionDeclarationDiskIoProfile] = {
    "high",
    "low",
    "standard",
}


def check_companion_declaration_disk_io_profile(value: str) -> CompanionDeclarationDiskIoProfile:
    if value in COMPANION_DECLARATION_DISK_IO_PROFILE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMPANION_DECLARATION_DISK_IO_PROFILE_VALUES!r}")
