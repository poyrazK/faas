from typing import Literal

DownloadProfileCaptureKind = Literal["block", "cpu", "heap", "mutex", "stacks"]

DOWNLOAD_PROFILE_CAPTURE_KIND_VALUES: set[DownloadProfileCaptureKind] = {
    "block",
    "cpu",
    "heap",
    "mutex",
    "stacks",
}


def check_download_profile_capture_kind(value: str) -> DownloadProfileCaptureKind:
    if value in DOWNLOAD_PROFILE_CAPTURE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOWNLOAD_PROFILE_CAPTURE_KIND_VALUES!r}")
