from typing import Literal

SidecarPreset = Literal["datadog-dogstatsd", "opentelemetry", "sentry"]

SIDECAR_PRESET_VALUES: set[SidecarPreset] = {
    "datadog-dogstatsd",
    "opentelemetry",
    "sentry",
}


def check_sidecar_preset(value: str) -> SidecarPreset:
    if value in SIDECAR_PRESET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SIDECAR_PRESET_VALUES!r}")
