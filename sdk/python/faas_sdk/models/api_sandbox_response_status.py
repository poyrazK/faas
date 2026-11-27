from typing import Literal

APISandboxResponseStatus = Literal[
    "ended",
    "failed",
    "live",
    "preparing_database",
    "running_migrations",
    "starting_release",
    "tearing_down",
    "verifying_api",
]

API_SANDBOX_RESPONSE_STATUS_VALUES: set[APISandboxResponseStatus] = {
    "ended",
    "failed",
    "live",
    "preparing_database",
    "running_migrations",
    "starting_release",
    "tearing_down",
    "verifying_api",
}


def check_api_sandbox_response_status(value: str) -> APISandboxResponseStatus:
    if value in API_SANDBOX_RESPONSE_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_SANDBOX_RESPONSE_STATUS_VALUES!r}")
