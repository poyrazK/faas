from typing import Literal

DiffAppConfigPatchAppProtocol = Literal["grpc", "http1", "http2"]

DIFF_APP_CONFIG_PATCH_APP_PROTOCOL_VALUES: set[DiffAppConfigPatchAppProtocol] = {
    "grpc",
    "http1",
    "http2",
}


def check_diff_app_config_patch_app_protocol(value: str) -> DiffAppConfigPatchAppProtocol:
    if value in DIFF_APP_CONFIG_PATCH_APP_PROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DIFF_APP_CONFIG_PATCH_APP_PROTOCOL_VALUES!r}")
