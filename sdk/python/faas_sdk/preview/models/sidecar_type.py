from typing import Literal

SidecarType = Literal["init", "sidecar"]

SIDECAR_TYPE_VALUES: set[SidecarType] = {
    "init",
    "sidecar",
}


def check_sidecar_type(value: str) -> SidecarType:
    if value in SIDECAR_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SIDECAR_TYPE_VALUES!r}")
