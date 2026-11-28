from typing import Literal

SecretRuntimeReloadObservationReloadSupport = Literal["disabled", "enabled", "unknown"]

SECRET_RUNTIME_RELOAD_OBSERVATION_RELOAD_SUPPORT_VALUES: set[SecretRuntimeReloadObservationReloadSupport] = {
    "disabled",
    "enabled",
    "unknown",
}


def check_secret_runtime_reload_observation_reload_support(value: str) -> SecretRuntimeReloadObservationReloadSupport:
    if value in SECRET_RUNTIME_RELOAD_OBSERVATION_RELOAD_SUPPORT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {SECRET_RUNTIME_RELOAD_OBSERVATION_RELOAD_SUPPORT_VALUES!r}"
    )
