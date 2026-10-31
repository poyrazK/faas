from typing import Literal

AppHealthResponsePhase = Literal[
    "deploying", "idle", "maintenance", "not_deployed", "serving", "starting", "stopped", "unknown", "unsupported"
]

APP_HEALTH_RESPONSE_PHASE_VALUES: set[AppHealthResponsePhase] = {
    "deploying",
    "idle",
    "maintenance",
    "not_deployed",
    "serving",
    "starting",
    "stopped",
    "unknown",
    "unsupported",
}


def check_app_health_response_phase(value: str) -> AppHealthResponsePhase:
    if value in APP_HEALTH_RESPONSE_PHASE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_HEALTH_RESPONSE_PHASE_VALUES!r}")
