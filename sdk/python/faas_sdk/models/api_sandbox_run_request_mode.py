from typing import Literal

APISandboxRunRequestMode = Literal["automated", "partner"]

API_SANDBOX_RUN_REQUEST_MODE_VALUES: set[APISandboxRunRequestMode] = {
    "automated",
    "partner",
}


def check_api_sandbox_run_request_mode(value: str) -> APISandboxRunRequestMode:
    if value in API_SANDBOX_RUN_REQUEST_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_RUN_REQUEST_MODE_VALUES!r}")
