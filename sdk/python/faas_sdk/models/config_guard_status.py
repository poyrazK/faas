from typing import Literal

ConfigGuardStatus = Literal["clean", "no_traffic", "regressed", "restored", "superseded", "watching"]

CONFIG_GUARD_STATUS_VALUES: set[ConfigGuardStatus] = {
    "clean",
    "no_traffic",
    "regressed",
    "restored",
    "superseded",
    "watching",
}


def check_config_guard_status(value: str) -> ConfigGuardStatus:
    if value in CONFIG_GUARD_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CONFIG_GUARD_STATUS_VALUES!r}")
